package authenticators

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/goccy/go-json"
	"github.com/rs/zerolog"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"

	"github.com/dadrus/heimdall/internal/cache"
	"github.com/dadrus/heimdall/internal/config"
	"github.com/dadrus/heimdall/internal/endpoint"
	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/pipeline/authenticators/extractors"
	"github.com/dadrus/heimdall/internal/pipeline/oauth2"
	"github.com/dadrus/heimdall/internal/pipeline/subject"
	"github.com/dadrus/heimdall/internal/truststore"
	"github.com/dadrus/heimdall/internal/x"
	"github.com/dadrus/heimdall/internal/x/errorchain"
	"github.com/dadrus/heimdall/internal/x/pkix"
)

const defaultJWTAuthenticatorTTL = 10 * time.Minute

// by intention. Used only during application bootstrap
// nolint
func init() {
	registerAuthenticatorTypeFactory(
		func(id string, typ config.PipelineObjectType, conf map[string]any) (bool, Authenticator, error) {
			if typ != config.POTJwt {
				return false, nil, nil
			}

			auth, err := newJwtAuthenticator(id, conf)

			return true, auth, err
		})
}

type jwtAuthenticator struct {
	id                   string
	e                    endpoint.Endpoint
	a                    oauth2.Expectation
	ttl                  *time.Duration
	sf                   SubjectFactory
	ads                  extractors.AuthDataExtractStrategy
	allowFallbackOnError bool
	trustStore           truststore.TrustStore
	validateJWKCert      bool
}

func newJwtAuthenticator(id string, rawConfig map[string]any) (*jwtAuthenticator, error) { // nolint: funlen
	type Config struct {
		Endpoint             endpoint.Endpoint                   `mapstructure:"jwks_endpoint"`
		AuthDataSource       extractors.CompositeExtractStrategy `mapstructure:"jwt_source"`
		Assertions           oauth2.Expectation                  `mapstructure:"assertions"`
		SubjectInfo          SubjectInfo                         `mapstructure:"subject"`
		CacheTTL             *time.Duration                      `mapstructure:"cache_ttl"`
		AllowFallbackOnError bool                                `mapstructure:"allow_fallback_on_error"`
		ValidateJWK          *bool                               `mapstructure:"validate_jwk"`
		TrustStore           truststore.TrustStore               `mapstructure:"trust_store"`
	}

	var (
		conf Config
		err  error
	)

	if err = decodeConfig(rawConfig, &conf); err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrConfiguration, "failed to unmarshal jwt authenticator config").
			CausedBy(err)
	}

	if err = conf.Endpoint.Validate(); err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrConfiguration, "failed to validate endpoint configuration").
			CausedBy(err)
	}

	if len(conf.Assertions.TrustedIssuers) == 0 {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrConfiguration, "no trusted issuers configured")
	}

	if conf.Endpoint.Headers == nil {
		conf.Endpoint.Headers = make(map[string]string)
	}

	if _, ok := conf.Endpoint.Headers["Accept-Type"]; !ok {
		conf.Endpoint.Headers["Accept-Type"] = "application/json"
	}

	if len(conf.Endpoint.Method) == 0 {
		conf.Endpoint.Method = "GET"
	}

	if len(conf.Assertions.AllowedAlgorithms) == 0 {
		conf.Assertions.AllowedAlgorithms = defaultAllowedAlgorithms()
	}

	if conf.Assertions.ScopesMatcher == nil {
		conf.Assertions.ScopesMatcher = oauth2.NoopMatcher{}
	}

	if len(conf.SubjectInfo.IDFrom) == 0 {
		conf.SubjectInfo.IDFrom = "sub"
	}

	validateJWKCert := x.IfThenElseExec(conf.ValidateJWK != nil,
		func() bool { return *conf.ValidateJWK },
		func() bool { return true })

	ads := x.IfThenElseExec(conf.AuthDataSource == nil,
		func() extractors.CompositeExtractStrategy {
			return extractors.CompositeExtractStrategy{
				extractors.HeaderValueExtractStrategy{Name: "Authorization", Schema: "Bearer"},
				extractors.QueryParameterExtractStrategy{Name: "access_token"},
				extractors.BodyParameterExtractStrategy{Name: "access_token"},
			}
		},
		func() extractors.CompositeExtractStrategy { return conf.AuthDataSource },
	)

	return &jwtAuthenticator{
		id:                   id,
		e:                    conf.Endpoint,
		a:                    conf.Assertions,
		ttl:                  conf.CacheTTL,
		sf:                   &conf.SubjectInfo,
		ads:                  ads,
		allowFallbackOnError: conf.AllowFallbackOnError,
		validateJWKCert:      validateJWKCert,
		trustStore:           conf.TrustStore,
	}, nil
}

func (a *jwtAuthenticator) Execute(ctx heimdall.Context) (*subject.Subject, error) {
	logger := zerolog.Ctx(ctx.AppContext())
	logger.Debug().Msg("Authenticating using JWT authenticator")

	jwtAd, err := a.ads.GetAuthData(ctx)
	if err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrAuthentication, "no JWT present").
			WithErrorContext(a).
			CausedBy(err)
	}

	token, err := jwt.ParseSigned(jwtAd.Value())
	if err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrAuthentication, "failed to parse JWT").
			WithErrorContext(a).
			CausedBy(heimdall.ErrArgument).
			CausedBy(err)
	}

	rawClaims, err := a.verifyToken(ctx, token)
	if err != nil {
		return nil, err
	}

	sub, err := a.sf.CreateSubject(rawClaims)
	if err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrInternal, "failed to extract subject information from jwt").
			WithErrorContext(a).
			CausedBy(err)
	}

	return sub, nil
}

func (a *jwtAuthenticator) WithConfig(config map[string]any) (Authenticator, error) {
	// this authenticator allows assertions and ttl to be redefined on the rule level
	if len(config) == 0 {
		return a, nil
	}

	type Config struct {
		Assertions           *oauth2.Expectation `mapstructure:"assertions"`
		CacheTTL             *time.Duration      `mapstructure:"cache_ttl"`
		AllowFallbackOnError *bool               `mapstructure:"allow_fallback_on_error"`
	}

	var conf Config
	if err := decodeConfig(config, &conf); err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrConfiguration, "failed to unmarshal jwt authenticator config").
			CausedBy(err)
	}

	return &jwtAuthenticator{
		id:  a.id,
		e:   a.e,
		a:   conf.Assertions.Merge(&a.a),
		ttl: x.IfThenElse(conf.CacheTTL != nil, conf.CacheTTL, a.ttl),
		sf:  a.sf,
		ads: a.ads,
		allowFallbackOnError: x.IfThenElseExec(conf.AllowFallbackOnError != nil,
			func() bool { return *conf.AllowFallbackOnError },
			func() bool { return a.allowFallbackOnError }),
		validateJWKCert: a.validateJWKCert,
		trustStore:      a.trustStore,
	}, nil
}

func (a *jwtAuthenticator) IsFallbackOnErrorAllowed() bool {
	return a.allowFallbackOnError
}

func (a *jwtAuthenticator) HandlerID() string {
	return a.id
}

func (a *jwtAuthenticator) isCacheEnabled() bool {
	// cache is enabled if ttl is not configured (in that case the ttl value from either
	// the jwk cert (if available) or the defaultTTL is used), or if ttl is configured and
	// the value > 0
	return a.ttl == nil || (a.ttl != nil && *a.ttl > 0)
}

func (a *jwtAuthenticator) getCacheTTL(key *jose.JSONWebKey) time.Duration {
	// timeLeeway defines the default time deviation to ensure the cert of the JWK is still valid
	// when used from cache
	const timeLeeway = 10

	if !a.isCacheEnabled() {
		return 0
	}

	// we cache by default using the settings in the certificate (if available)
	// or based on ttl. Latter overwrites the settings in the certificate
	// if it is shorter than the ttl of the certificate
	certTTL := x.IfThenElseExec(len(key.Certificates) != 0,
		func() time.Duration {
			expiresIn := key.Certificates[0].NotAfter.Unix() - time.Now().Unix() - timeLeeway

			return x.IfThenElse(expiresIn > 0, time.Duration(expiresIn)*time.Second, 0)
		},
		func() time.Duration { return 0 })

	configuredTTL := x.IfThenElseExec(a.ttl != nil,
		func() time.Duration { return *a.ttl },
		func() time.Duration { return defaultJWTAuthenticatorTTL })

	switch {
	case configuredTTL == 0 && certTTL == 0:
		return 0
	case configuredTTL == 0 && certTTL != 0:
		return certTTL
	case configuredTTL != 0 && certTTL == 0:
		return configuredTTL
	default:
		return x.IfThenElse(configuredTTL < certTTL, configuredTTL, certTTL)
	}
}

func (a *jwtAuthenticator) verifyToken(ctx heimdall.Context, token *jwt.JSONWebToken) (json.RawMessage, error) {
	if len(token.Headers[0].KeyID) == 0 {
		return a.verifyTokenWithoutKID(ctx, token)
	}

	sigKey, err := a.getKey(ctx, token.Headers[0].KeyID)
	if err != nil {
		return nil, err
	}

	return a.verifyTokenWithKey(token, sigKey)
}

func (a *jwtAuthenticator) verifyTokenWithoutKID(ctx heimdall.Context, token *jwt.JSONWebToken) (
	json.RawMessage, error,
) {
	logger := zerolog.Ctx(ctx.AppContext())
	logger.Info().Msg("No kid present in the JWT")

	var rawClaims json.RawMessage

	jwks, err := a.fetchJWKS(ctx)
	if err != nil {
		return nil, err
	}

	for idx := range jwks.Keys {
		sigKey := jwks.Keys[idx]
		if err = a.validateJWK(&sigKey); err != nil {
			logger.Info().Err(err).Str("_key_id", sigKey.KeyID).Msg("JWK is invalid")

			continue
		}

		rawClaims, err = a.verifyTokenWithKey(token, &sigKey)
		if err == nil {
			break
		} else {
			logger.Info().Err(err).Str("_key_id", sigKey.KeyID).Msg("Failed to verify JWT")
		}
	}

	if len(rawClaims) == 0 {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrAuthentication,
				"None of the keys received from the JWKS endpoint could be used to verify the JWT").
			WithErrorContext(a)
	}

	return rawClaims, nil
}

func (a *jwtAuthenticator) getKey(ctx heimdall.Context, keyID string) (*jose.JSONWebKey, error) {
	cch := cache.Ctx(ctx.AppContext())
	logger := zerolog.Ctx(ctx.AppContext())

	var (
		cacheKey   string
		cacheEntry any
		jwk        *jose.JSONWebKey
		jwks       *jose.JSONWebKeySet
		err        error
		ok         bool
	)

	if a.isCacheEnabled() {
		cacheKey = a.calculateCacheKey(keyID)
		cacheEntry = cch.Get(cacheKey)
	}

	if cacheEntry != nil {
		if jwk, ok = cacheEntry.(*jose.JSONWebKey); !ok {
			logger.Warn().Msg("Wrong object type from cache")
			cch.Delete(cacheKey)
		} else {
			logger.Debug().Msg("Reusing JWK from cache")
		}
	}

	if jwk != nil {
		return jwk, nil
	}

	jwks, err = a.fetchJWKS(ctx)
	if err != nil {
		return nil, err
	}

	keys := jwks.Key(keyID)
	if len(keys) != 1 {
		return nil, errorchain.
			NewWithMessagef(heimdall.ErrAuthentication,
				"no (unique) key found for the keyID='%s' referenced in the JWT", keyID).
			WithErrorContext(a)
	}

	jwk = &keys[0]
	if err = a.validateJWK(jwk); err != nil {
		return nil, errorchain.
			NewWithMessagef(heimdall.ErrAuthentication, "JWK for keyID=%s is invalid", keyID).
			WithErrorContext(a).
			CausedBy(err)
	}

	if cacheTTL := a.getCacheTTL(jwk); cacheTTL > 0 {
		cch.Set(cacheKey, jwk, cacheTTL)
	}

	return jwk, nil
}

func (a *jwtAuthenticator) fetchJWKS(ctx heimdall.Context) (*jose.JSONWebKeySet, error) {
	logger := zerolog.Ctx(ctx.AppContext())

	logger.Debug().Msg("Retrieving JWKS from configured endpoint")

	req, err := a.e.CreateRequest(ctx.AppContext(), nil, nil)
	if err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrInternal, "failed creating request").
			WithErrorContext(a).
			CausedBy(err)
	}

	resp, err := a.e.CreateClient(req.URL.Hostname()).Do(req)
	if err != nil {
		var clientErr *url.Error
		if errors.As(err, &clientErr) && clientErr.Timeout() {
			return nil, errorchain.
				NewWithMessage(heimdall.ErrCommunicationTimeout, "request to JWKS endpoint timed out").
				WithErrorContext(a).
				CausedBy(err)
		}

		return nil, errorchain.
			NewWithMessage(heimdall.ErrCommunication, "request to JWKS endpoint failed").
			WithErrorContext(a).
			CausedBy(err)
	}

	defer resp.Body.Close()

	return a.readJWKS(resp)
}

func (a *jwtAuthenticator) readJWKS(resp *http.Response) (*jose.JSONWebKeySet, error) {
	if !(resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices) {
		return nil, errorchain.
			NewWithMessagef(heimdall.ErrCommunication, "unexpected response. code: %v", resp.StatusCode).
			WithErrorContext(a)
	}

	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrInternal, "failed to read response").
			WithErrorContext(a).
			CausedBy(err)
	}

	// unmarshal the received key set
	var jwks jose.JSONWebKeySet
	if err := json.Unmarshal(rawData, &jwks); err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrInternal, "failed to unmarshal received jwks").
			WithErrorContext(a).
			CausedBy(err)
	}

	return &jwks, nil
}

func (a *jwtAuthenticator) verifyTokenWithKey(token *jwt.JSONWebToken, key *jose.JSONWebKey) (json.RawMessage, error) {
	header := token.Headers[0]

	if len(header.Algorithm) != 0 && key.Algorithm != header.Algorithm {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrAuthentication,
				"algorithm in the JWT header does not match the algorithm referenced in the key").
			WithErrorContext(a)
	}

	if err := a.a.AssertAlgorithm(key.Algorithm); err != nil {
		return nil, errorchain.
			NewWithMessagef(heimdall.ErrAuthentication, "%s algorithm is not allowed", key.Algorithm).
			WithErrorContext(a).
			CausedBy(err)
	}

	var (
		mapClaims map[string]interface{}
		claims    oauth2.Claims
	)

	if err := token.Claims(key, &mapClaims, &claims); err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrAuthentication, "failed to verify JWT signature").
			WithErrorContext(a).
			CausedBy(err)
	}

	if err := claims.Validate(a.a); err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrAuthentication, "access token does not satisfy assertion conditions").
			WithErrorContext(a).
			CausedBy(err)
	}

	rawPayload, err := json.Marshal(mapClaims)
	if err != nil {
		return nil, errorchain.
			NewWithMessage(heimdall.ErrInternal, "failed to marshal jwt payload").
			WithErrorContext(a).
			CausedBy(err)
	}

	return rawPayload, nil
}

func (a *jwtAuthenticator) calculateCacheKey(reference string) string {
	digest := sha256.New()
	digest.Write([]byte(a.e.Hash()))
	digest.Write([]byte(reference))

	return hex.EncodeToString(digest.Sum(nil))
}

func (a *jwtAuthenticator) validateJWK(jwk *jose.JSONWebKey) error {
	if !a.validateJWKCert || len(jwk.Certificates) == 0 {
		return nil
	}

	return pkix.ValidateCertificate(jwk.Certificates[0],
		pkix.WithIntermediateCACertificates(jwk.Certificates[1:]),
		pkix.WithKeyUsage(x509.KeyUsageDigitalSignature),
		x.IfThenElseExec(len(a.trustStore) == 0,
			pkix.WithSystemTrustStore,
			func() pkix.ValidationOption { return pkix.WithRootCACertificates(a.trustStore) }),
	)
}
