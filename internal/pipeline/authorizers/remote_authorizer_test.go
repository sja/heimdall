package authorizers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/cache"
	"github.com/dadrus/heimdall/internal/cache/mocks"
	"github.com/dadrus/heimdall/internal/endpoint"
	"github.com/dadrus/heimdall/internal/heimdall"
	heimdallmocks "github.com/dadrus/heimdall/internal/heimdall/mocks"
	"github.com/dadrus/heimdall/internal/pipeline/script"
	"github.com/dadrus/heimdall/internal/pipeline/subject"
	"github.com/dadrus/heimdall/internal/pipeline/template"
	"github.com/dadrus/heimdall/internal/testsupport"
	"github.com/dadrus/heimdall/internal/x"
	"github.com/dadrus/heimdall/internal/x/errorchain"
)

func TestCreateRemoteAuthorizer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc     string
		id     string
		config []byte
		assert func(t *testing.T, err error, auth *remoteAuthorizer)
	}{
		{
			uc: "configuration with unknown properties",
			config: []byte(`
endpoint:
  url: http://foo.bar
foo: bar
`),
			assert: func(t *testing.T, err error, auth *remoteAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc: "configuration with invalid endpoint config",
			config: []byte(`
endpoint:
  method: FOO
payload: FooBar
`),
			assert: func(t *testing.T, err error, auth *remoteAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to validate endpoint")
			},
		},
		{
			uc: "configuration without both payload and header",
			config: []byte(`
endpoint:
  url: http://foo.bar
`),
			assert: func(t *testing.T, err error, auth *remoteAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "either a payload or at least")
			},
		},
		{
			uc: "configuration with endpoint and payload",
			id: "authz",
			config: []byte(`
endpoint:
  url: http://foo.bar
payload: "{{ .Subject.ID }}"
`),
			assert: func(t *testing.T, err error, auth *remoteAuthorizer) {
				t.Helper()

				require.NoError(t, err)

				require.NotNil(t, auth)
				require.NotNil(t, auth.payload)
				val, err := auth.payload.Render(nil, &subject.Subject{ID: "bar"})
				require.NoError(t, err)
				assert.Equal(t, "bar", val)
				assert.Empty(t, auth.headersForUpstream)
				assert.Zero(t, auth.ttl)

				assert.Equal(t, "authz", auth.HandlerID())
			},
		},
		{
			uc: "full configuration",
			id: "authz",
			config: []byte(`
endpoint:
  url: http://foo.bar
payload: "{{ .Subject.ID }}"
script: "throw 'foobar'"
forward_response_headers_to_upstream:
  - Foo
  - Bar
cache_ttl: 5s
`),
			assert: func(t *testing.T, err error, auth *remoteAuthorizer) {
				t.Helper()

				ctx := &heimdallmocks.MockContext{}
				ctx.On("AppContext").Return(context.Background())

				require.NoError(t, err)

				require.NotNil(t, auth)
				require.NotNil(t, auth.payload)
				val, err := auth.payload.Render(nil, &subject.Subject{ID: "bar"})
				require.NoError(t, err)
				require.NotNil(t, auth.script)
				_, err = auth.script.ExecuteOnPayload(ctx, nil)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "foobar")
				assert.Equal(t, "bar", val)
				assert.Len(t, auth.headersForUpstream, 2)
				assert.Contains(t, auth.headersForUpstream, "Foo")
				assert.Contains(t, auth.headersForUpstream, "Bar")
				assert.NotNil(t, auth.ttl)
				assert.Equal(t, 5*time.Second, auth.ttl)

				assert.Equal(t, "authz", auth.HandlerID())
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			// WHEN
			auth, err := newRemoteAuthorizer(tc.id, conf)

			// THEN
			tc.assert(t, err, auth)
		})
	}
}

func TestCreateRemoteAuthorizerFromPrototype(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc              string
		id              string
		prototypeConfig []byte
		config          []byte
		assert          func(t *testing.T, err error, prototype *remoteAuthorizer, configured *remoteAuthorizer)
	}{
		{
			uc: "without new configuration",
			id: "authz1",
			prototypeConfig: []byte(`
endpoint:
  url: http://foo.bar
payload: bar
`),
			assert: func(t *testing.T, err error, prototype *remoteAuthorizer, configured *remoteAuthorizer) {
				t.Helper()

				require.NoError(t, err)

				assert.Equal(t, prototype, configured)
				assert.Equal(t, "authz1", configured.HandlerID())
			},
		},
		{
			uc: "with empty configuration",
			id: "authz2",
			prototypeConfig: []byte(`
endpoint:
  url: http://foo.bar
payload: bar
`),
			config: []byte(``),
			assert: func(t *testing.T, err error, prototype *remoteAuthorizer, configured *remoteAuthorizer) {
				t.Helper()

				require.NoError(t, err)

				assert.Equal(t, prototype, configured)
				assert.Equal(t, "authz2", configured.HandlerID())
			},
		},
		{
			uc: "configuration with unknown properties",
			prototypeConfig: []byte(`
endpoint:
  url: http://foo.bar
payload: bar
`),
			config: []byte(`
foo: bar
`),
			assert: func(t *testing.T, err error, prototype *remoteAuthorizer, configured *remoteAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc: "with overridden empty payload",
			id: "authz3",
			prototypeConfig: []byte(`
endpoint:
  url: http://foo.bar
payload: bar
`),
			config: []byte(`
payload: ""
cache_ttl: 1s
`),
			assert: func(t *testing.T, err error, prototype *remoteAuthorizer, configured *remoteAuthorizer) {
				t.Helper()

				require.NoError(t, err)

				assert.NotEqual(t, prototype, configured)
				assert.NotNil(t, configured)
				assert.Equal(t, prototype.e, configured.e)
				assert.Equal(t, prototype.id, configured.id)
				assert.Equal(t, prototype.payload, configured.payload)
				assert.Equal(t, prototype.script, configured.script)
				assert.Empty(t, configured.headersForUpstream)
				assert.NotNil(t, configured.ttl)
				assert.Equal(t, "authz3", configured.HandlerID())
			},
		},
		{
			uc: "with everything possible reconfigured",
			id: "authz4",
			prototypeConfig: []byte(`
endpoint:
  url: http://foo.bar
  headers:
    Foo: Bar
`),
			config: []byte(`
payload: Baz
forward_response_headers_to_upstream:
  - Bar
  - Foo
script: "throw 'foobar'"
cache_ttl: 15s
`),
			assert: func(t *testing.T, err error, prototype *remoteAuthorizer, configured *remoteAuthorizer) {
				t.Helper()

				ctx := &heimdallmocks.MockContext{}
				ctx.On("AppContext").Return(context.Background())

				require.NoError(t, err)

				assert.NotEqual(t, prototype, configured)
				assert.NotNil(t, configured)
				assert.Equal(t, prototype.e, configured.e)
				assert.Equal(t, prototype.id, configured.id)
				require.NotNil(t, configured.payload)
				val, err := configured.payload.Render(nil, nil)
				require.NoError(t, err)
				assert.Nil(t, prototype.script)
				require.NotNil(t, configured.script)
				_, err = configured.script.ExecuteOnPayload(ctx, nil)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "foobar")
				assert.Equal(t, "Baz", val)
				assert.Len(t, configured.headersForUpstream, 2)
				assert.Contains(t, configured.headersForUpstream, "Bar")
				assert.Contains(t, configured.headersForUpstream, "Foo")
				assert.Equal(t, 15*time.Second, configured.ttl)

				assert.NotEqual(t, prototype.ttl, configured.ttl)
				assert.NotEqual(t, prototype.headersForUpstream, configured.headersForUpstream)
				assert.NotEqual(t, prototype.payload, configured.payload)
				assert.Equal(t, "authz4", configured.HandlerID())
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			pc, err := testsupport.DecodeTestConfig(tc.prototypeConfig)
			require.NoError(t, err)

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			prototype, err := newRemoteAuthorizer(tc.id, pc)
			require.NoError(t, err)

			// WHEN
			auth, err := prototype.WithConfig(conf)

			// THEN
			var (
				locAuth *remoteAuthorizer
				ok      bool
			)

			if err == nil {
				locAuth, ok = auth.(*remoteAuthorizer)
				require.True(t, ok)
			}

			tc.assert(t, err, prototype, locAuth)
		})
	}
}

// nolint: maintidx
func TestRemoteAuthorizerExecute(t *testing.T) {
	t.Parallel()

	var (
		authorizationEndpointCalled bool
		checkRequest                func(req *http.Request)

		responseHeaders     map[string]string
		responseContentType string
		responseContent     []byte
		responseCode        int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizationEndpointCalled = true

		checkRequest(r)

		for hn, hv := range responseHeaders {
			w.Header().Set(hn, hv)
		}

		if responseContent != nil {
			w.Header().Set("Content-Type", responseContentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(responseContent)))
			_, err := w.Write(responseContent)
			assert.NoError(t, err)
		}

		w.WriteHeader(responseCode)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		uc               string
		authorizer       *remoteAuthorizer
		subject          *subject.Subject
		instructServer   func(t *testing.T)
		configureContext func(t *testing.T, ctx *heimdallmocks.MockContext)
		configureCache   func(t *testing.T, cch *mocks.MockCache, authorizer *remoteAuthorizer, sub *subject.Subject)
		assert           func(t *testing.T, err error, sub *subject.Subject)
	}{
		{
			uc: "successful with payload and with header, without payload from server and without header " +
				"forwarding and with disabled cache",
			authorizer: &remoteAuthorizer{
				e: endpoint.Endpoint{
					URL:     srv.URL,
					Headers: map[string]string{"Foo-Bar": "{{ .Subject.Attributes.bar }}"},
				},
				payload: func() template.Template {
					tpl, _ := template.New("{{ .Subject.ID }}")

					return tpl
				}(),
			},
			subject: &subject.Subject{
				ID:         "my-id",
				Attributes: map[string]any{"bar": "baz"},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				responseCode = http.StatusOK

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "baz", req.Header.Get("Foo-Bar"))
					assert.Empty(t, req.Header.Get("Content-Type"))
					assert.Empty(t, req.Header.Get("Accept"))

					data, err := io.ReadAll(req.Body)
					assert.NoError(t, err)

					assert.Equal(t, "my-id", string(data))
				}
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.True(t, authorizationEndpointCalled)
				assert.Len(t, sub.Attributes, 1)
				assert.Equal(t, "baz", sub.Attributes["bar"])
			},
		},
		{
			uc: "successful with json payload and with header, with json payload from server and with header" +
				" forwarding and with disabled cache",
			authorizer: &remoteAuthorizer{
				id: "authorizer",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/json",
						"Accept":       "application/json",
						"Foo-Bar":      "{{ .Subject.Attributes.bar }}",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`{ "user_id": {{ quote .Subject.ID }} }`)

					return tpl
				}(),
				headersForUpstream: []string{"X-Foo-Bar", "X-Bar-Foo"},
			},
			subject: &subject.Subject{
				ID:         "my-id",
				Attributes: map[string]any{"bar": "baz"},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "baz", req.Header.Get("Foo-Bar"))
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					assert.Equal(t, "application/json", req.Header.Get("Accept"))

					data, err := io.ReadAll(req.Body)
					assert.NoError(t, err)

					var mapData map[string]string

					err = json.Unmarshal(data, &mapData)
					require.NoError(t, err)

					assert.Len(t, mapData, 1)
					assert.Equal(t, "my-id", mapData["user_id"])
				}

				responseCode = http.StatusOK
				rawData, err := json.Marshal(map[string]any{
					"access_granted": true,
					"permissions":    []string{"read_foo", "write_foo"},
					"groups":         []string{"Foo-Users"},
				})
				require.NoError(t, err)
				responseContent = rawData
				responseContentType = "application/json"
				responseHeaders = map[string]string{"X-Foo-Bar": "HeyFoo"}
			},
			configureContext: func(t *testing.T, ctx *heimdallmocks.MockContext) {
				t.Helper()

				ctx.On("AddHeaderForUpstream", "X-Foo-Bar", "HeyFoo")
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.True(t, authorizationEndpointCalled)
				assert.Len(t, sub.Attributes, 2)
				assert.Equal(t, "baz", sub.Attributes["bar"])

				attrs := sub.Attributes["authorizer"]
				assert.NotEmpty(t, attrs)
				authorizerAttrs, ok := attrs.(map[string]any)
				require.True(t, ok)
				assert.Len(t, authorizerAttrs, 3)
				assert.Equal(t, true, authorizerAttrs["access_granted"])
				assert.Len(t, authorizerAttrs["permissions"], 2)
				assert.Contains(t, authorizerAttrs["permissions"], "read_foo")
				assert.Contains(t, authorizerAttrs["permissions"], "write_foo")
				assert.Len(t, authorizerAttrs["groups"], 1)
				assert.Contains(t, authorizerAttrs["groups"], "Foo-Users")
			},
		},
		{
			uc: "successful with www-form-urlencoded payload and without header, without payload from server " +
				"and with header forwarding and with failing cache hit",
			authorizer: &remoteAuthorizer{
				id: "authorizer",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/x-www-form-urlencoded",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`user_id={{ urlenc .Subject.ID }}&{{ .Subject.Attributes.bar }}=foo`)

					return tpl
				}(),
				headersForUpstream: []string{"X-Foo-Bar", "X-Bar-Foo"},
				ttl:                20 * time.Second,
			},
			subject: &subject.Subject{
				ID:         "my id",
				Attributes: map[string]any{"bar": "baz"},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))

					data, err := io.ReadAll(req.Body)
					assert.NoError(t, err)

					formValues, err := url.ParseQuery(string(data))
					require.NoError(t, err)

					assert.Len(t, formValues, 2)
					assert.Equal(t, []string{"my id"}, formValues["user_id"])
					assert.Equal(t, []string{"foo"}, formValues["baz"])
				}

				responseCode = http.StatusOK
				responseHeaders = map[string]string{"X-Foo-Bar": "HeyFoo"}
			},
			configureContext: func(t *testing.T, ctx *heimdallmocks.MockContext) {
				t.Helper()

				ctx.On("AddHeaderForUpstream", "X-Foo-Bar", "HeyFoo")
			},
			configureCache: func(t *testing.T, cch *mocks.MockCache, auth *remoteAuthorizer, sub *subject.Subject) {
				t.Helper()

				cacheKey, err := auth.calculateCacheKey(sub)
				require.NoError(t, err)

				cch.On("Get", cacheKey).Return(nil)
				cch.On("Set", cacheKey,
					mock.MatchedBy(func(val *authorizationInformation) bool {
						return val != nil && val.payload == nil && len(val.headers.Get("X-Foo-Bar")) != 0
					}), auth.ttl)
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.True(t, authorizationEndpointCalled)
				assert.Len(t, sub.Attributes, 1)
				assert.Equal(t, "baz", sub.Attributes["bar"])

				assert.Empty(t, sub.Attributes["authorizer"])
			},
		},
		{
			uc: "successful without headers and payload and with cache",
			authorizer: &remoteAuthorizer{
				id: "authorizer",
				e: endpoint.Endpoint{
					URL:     fmt.Sprintf("%s/{{ .Subject.ID }}", srv.URL),
					Headers: map[string]string{"Accept": "application/x-www-form-urlencoded"},
				},
				ttl: 10 * time.Second,
			},
			subject: &subject.Subject{
				ID:         "foobar",
				Attributes: map[string]any{"bar": "baz"},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Accept"))
					assert.True(t, strings.HasSuffix(req.URL.Path, "/foobar"))
				}

				responseCode = http.StatusOK
			},
			configureCache: func(t *testing.T, cch *mocks.MockCache, auth *remoteAuthorizer, sub *subject.Subject) {
				t.Helper()

				cacheKey, err := auth.calculateCacheKey(sub)
				require.NoError(t, err)

				cch.On("Get", cacheKey).Return(nil)
				cch.On("Set", cacheKey, mock.Anything, auth.ttl)
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.True(t, authorizationEndpointCalled)
				assert.Len(t, sub.Attributes, 1)
				assert.Equal(t, "baz", sub.Attributes["bar"])

				assert.Empty(t, sub.Attributes["authorizer"])
			},
		},
		{
			uc: "successfully reuse cache",
			authorizer: &remoteAuthorizer{
				id: "authorizer",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/x-www-form-urlencoded",
						"Foo-Bar":      "{{ .Subject.Attributes.bar }}",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`user_id={{ urlenc .Subject.ID }}&{{ urlenc .Subject.Attributes.bar }}=foo`)

					return tpl
				}(),
				headersForUpstream: []string{"X-Foo-Bar", "X-Bar-Foo"},
				ttl:                20 * time.Second,
			},
			subject: &subject.Subject{
				ID:         "my id",
				Attributes: map[string]any{"bar": "baz"},
			},
			configureContext: func(t *testing.T, ctx *heimdallmocks.MockContext) {
				t.Helper()

				ctx.On("AddHeaderForUpstream", "X-Foo-Bar", "HeyFoo")
				ctx.On("AddHeaderForUpstream", "X-Bar-Foo", "HeyBar")
			},
			configureCache: func(t *testing.T, cch *mocks.MockCache, auth *remoteAuthorizer, sub *subject.Subject) {
				t.Helper()

				cch.On("Get", mock.Anything).Return(&authorizationInformation{
					headers: http.Header{
						"X-Foo-Bar": {"HeyFoo"},
						"X-Bar-Foo": {"HeyBar"},
					},
					payload: map[string]string{"foo": "bar"},
				})
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.False(t, authorizationEndpointCalled)
				assert.Len(t, sub.Attributes, 2)
				assert.Equal(t, "baz", sub.Attributes["bar"])

				attrs := sub.Attributes["authorizer"]
				assert.NotEmpty(t, attrs)
				authorizerAttrs, ok := attrs.(map[string]string)
				require.True(t, ok)
				assert.Len(t, authorizerAttrs, 1)
				assert.Equal(t, "bar", authorizerAttrs["foo"])
			},
		},
		{
			uc: "cache with bad object in cache",
			authorizer: &remoteAuthorizer{
				id: "authorizer",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/x-www-form-urlencoded",
						"Foo-Bar":      "{{ .Subject.Attributes.bar }}",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`user_id={{ urlenc .Subject.ID }}&{{ urlenc .Subject.Attributes.bar }}=foo`)

					return tpl
				}(),
				headersForUpstream: []string{"X-Foo-Bar", "X-Bar-Foo"},
				ttl:                20 * time.Second,
			},
			subject: &subject.Subject{
				ID:         "my id",
				Attributes: map[string]any{"bar": "baz"},
			},
			configureContext: func(t *testing.T, ctx *heimdallmocks.MockContext) {
				t.Helper()

				ctx.On("AddHeaderForUpstream", "X-Foo-Bar", "HeyFoo")
			},
			configureCache: func(t *testing.T, cch *mocks.MockCache, auth *remoteAuthorizer, sub *subject.Subject) {
				t.Helper()

				cch.On("Get", mock.Anything).Return("Hello Foo")
				cch.On("Delete", mock.Anything)
				cch.On("Set", mock.Anything, mock.Anything, auth.ttl)
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				responseCode = http.StatusOK
				responseHeaders = map[string]string{"X-Foo-Bar": "HeyFoo"}

				rawData, err := json.Marshal(map[string]any{
					"access_granted": true,
				})
				require.NoError(t, err)
				responseContent = rawData
				responseContentType = "application/json"
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.True(t, authorizationEndpointCalled)
				assert.Len(t, sub.Attributes, 2)
				assert.Equal(t, "baz", sub.Attributes["bar"])

				assert.Len(t, sub.Attributes["authorizer"], 1)
			},
		},
		{
			uc: "with failed authorization",
			authorizer: &remoteAuthorizer{
				id: "authz",
				e: endpoint.Endpoint{
					URL:     srv.URL,
					Headers: map[string]string{"X-User-ID": "{{ .Subject.ID }}"},
				},
			},
			subject: &subject.Subject{ID: "foo"},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					assert.Equal(t, "foo", req.Header.Get("X-User-ID"))
				}

				responseCode = http.StatusUnauthorized
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.Error(t, err)

				assert.ErrorIs(t, err, heimdall.ErrAuthorization)
				assert.Contains(t, err.Error(), "authorization failed")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz", identifier.HandlerID())
			},
		},
		{
			uc: "with unsupported response content type",
			authorizer: &remoteAuthorizer{
				id: "foo",
				e: endpoint.Endpoint{
					URL:     srv.URL,
					Headers: map[string]string{"X-User-ID": "{{ .Subject.ID }}"},
				},
			},
			subject: &subject.Subject{ID: "foo", Attributes: map[string]any{}},
			instructServer: func(t *testing.T) {
				t.Helper()

				responseContent = []byte("Hi Foo")
				responseContentType = "text/text"
				responseCode = http.StatusOK
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.NoError(t, err)

				assert.True(t, authorizationEndpointCalled)
				assert.Equal(t, "Hi Foo", sub.Attributes["foo"])
			},
		},
		{
			uc: "with communication error (dns)",
			authorizer: &remoteAuthorizer{
				id: "authz",
				e:  endpoint.Endpoint{URL: "http://heimdall.test.local"},
				payload: func() template.Template {
					tpl, _ := template.New("bar")

					return tpl
				}(),
			},
			subject: &subject.Subject{ID: "foo"},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrCommunication)
				assert.Contains(t, err.Error(), "endpoint failed")

				assert.False(t, authorizationEndpointCalled)

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz", identifier.HandlerID())
			},
		},
		{
			uc:         "with error due to nil subject",
			authorizer: &remoteAuthorizer{id: "authz"},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				assert.False(t, authorizationEndpointCalled)

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrInternal)
				assert.Contains(t, err.Error(), "due to 'nil' subject")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz", identifier.HandlerID())
			},
		},
		{
			uc: "with script, which fails throwing error",
			authorizer: &remoteAuthorizer{
				id: "authz",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/json",
						"Accept":       "application/json",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`{ "user_id": {{ quote .Subject.ID }} }`)

					return tpl
				}(),
				script: func() script.Script {
					ecma := &mockScript{}
					ecma.On("ExecuteOnPayload", mock.Anything, mock.Anything).
						Return(nil, errorchain.NewWithMessage(heimdall.ErrInternal, "test"))

					return ecma
				}(),
			},
			subject: &subject.Subject{
				ID:         "my-id",
				Attributes: map[string]any{},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					assert.Equal(t, "application/json", req.Header.Get("Accept"))

					data, err := io.ReadAll(req.Body)
					assert.NoError(t, err)

					var mapData map[string]string

					err = json.Unmarshal(data, &mapData)
					require.NoError(t, err)

					assert.Len(t, mapData, 1)
					assert.Equal(t, "my-id", mapData["user_id"])
				}

				responseCode = http.StatusOK
				rawData, err := json.Marshal(map[string]any{
					"access_granted": true,
					"permissions":    []string{"read_foo", "write_foo"},
					"groups":         []string{"Foo-Users"},
				})
				require.NoError(t, err)
				responseContent = rawData
				responseContentType = "application/json"
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				assert.True(t, authorizationEndpointCalled)

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrAuthorization)
				assert.Contains(t, err.Error(), "test")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz", identifier.HandlerID())
			},
		},
		{
			uc: "with script, which returns false",
			authorizer: &remoteAuthorizer{
				id: "authz",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/json",
						"Accept":       "application/json",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`{ "user_id": {{ quote .Subject.ID }} }`)

					return tpl
				}(),
				script: func() script.Script {
					ecma := &mockScript{}
					ecma.On("ExecuteOnPayload", mock.Anything, mock.Anything).
						Return(boolValue(false), nil)

					return ecma
				}(),
			},
			subject: &subject.Subject{
				ID:         "my-id",
				Attributes: map[string]any{},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					assert.Equal(t, "application/json", req.Header.Get("Accept"))

					data, err := io.ReadAll(req.Body)
					assert.NoError(t, err)

					var mapData map[string]string

					err = json.Unmarshal(data, &mapData)
					require.NoError(t, err)

					assert.Len(t, mapData, 1)
					assert.Equal(t, "my-id", mapData["user_id"])
				}

				responseCode = http.StatusOK
				rawData, err := json.Marshal(map[string]any{
					"access_granted": true,
					"permissions":    []string{"read_foo", "write_foo"},
					"groups":         []string{"Foo-Users"},
				})
				require.NoError(t, err)
				responseContent = rawData
				responseContentType = "application/json"
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				assert.True(t, authorizationEndpointCalled)

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrAuthorization)
				assert.Contains(t, err.Error(), "script returned false")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz", identifier.HandlerID())
			},
		},
		{
			uc: "with script, which succeeds",
			authorizer: &remoteAuthorizer{
				id: "authorizer",
				e: endpoint.Endpoint{
					URL: srv.URL,
					Headers: map[string]string{
						"Content-Type": "application/json",
						"Accept":       "application/json",
					},
				},
				payload: func() template.Template {
					tpl, _ := template.New(`{ "user_id": {{ quote .Subject.ID }} }`)

					return tpl
				}(),
				script: func() script.Script {
					ecma := &mockScript{}
					ecma.On("ExecuteOnPayload", mock.Anything, mock.MatchedBy(
						func(data map[string]any) bool { return data["access_granted"] == true })).
						Return(boolValue(true), nil)

					return ecma
				}(),
			},
			subject: &subject.Subject{
				ID:         "my-id",
				Attributes: map[string]any{},
			},
			instructServer: func(t *testing.T) {
				t.Helper()

				checkRequest = func(req *http.Request) {
					t.Helper()

					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
					assert.Equal(t, "application/json", req.Header.Get("Accept"))

					data, err := io.ReadAll(req.Body)
					assert.NoError(t, err)

					var mapData map[string]string

					err = json.Unmarshal(data, &mapData)
					require.NoError(t, err)

					assert.Len(t, mapData, 1)
					assert.Equal(t, "my-id", mapData["user_id"])
				}

				responseCode = http.StatusOK
				rawData, err := json.Marshal(map[string]any{
					"access_granted": true,
					"permissions":    []string{"read_foo", "write_foo"},
					"groups":         []string{"Foo-Users"},
				})
				require.NoError(t, err)
				responseContent = rawData
				responseContentType = "application/json"
			},
			assert: func(t *testing.T, err error, sub *subject.Subject) {
				t.Helper()

				assert.True(t, authorizationEndpointCalled)

				require.NoError(t, err)

				require.Len(t, sub.Attributes, 1)
				attrs := sub.Attributes["authorizer"]
				assert.NotEmpty(t, attrs)
				authorizerAttrs, ok := attrs.(map[string]any)
				require.True(t, ok)
				assert.Len(t, authorizerAttrs, 3)
				assert.Equal(t, true, authorizerAttrs["access_granted"])
				assert.Len(t, authorizerAttrs["permissions"], 2)
				assert.Contains(t, authorizerAttrs["permissions"], "read_foo")
				assert.Contains(t, authorizerAttrs["permissions"], "write_foo")
				assert.Len(t, authorizerAttrs["groups"], 1)
				assert.Contains(t, authorizerAttrs["groups"], "Foo-Users")
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			authorizationEndpointCalled = false
			responseHeaders = nil
			responseContentType = ""
			responseContent = nil

			checkRequest = func(*http.Request) { t.Helper() }

			instructServer := x.IfThenElse(tc.instructServer != nil,
				tc.instructServer,
				func(t *testing.T) { t.Helper() })

			configureContext := x.IfThenElse(tc.configureContext != nil,
				tc.configureContext,
				func(t *testing.T, ctx *heimdallmocks.MockContext) { t.Helper() })

			configureCache := x.IfThenElse(tc.configureCache != nil,
				tc.configureCache,
				func(t *testing.T, ctx *mocks.MockCache, auth *remoteAuthorizer, sub *subject.Subject) {
					t.Helper()
				})

			cch := &mocks.MockCache{}

			ctx := &heimdallmocks.MockContext{}
			ctx.On("AppContext").Return(cache.WithContext(context.Background(), cch))

			configureContext(t, ctx)
			configureCache(t, cch, tc.authorizer, tc.subject)
			instructServer(t)

			// WHEN
			err := tc.authorizer.Execute(ctx, tc.subject)

			// THEN
			tc.assert(t, err, tc.subject)

			ctx.AssertExpectations(t)
			cch.AssertExpectations(t)
		})
	}
}

type boolValue bool

func (v boolValue) ToInteger() int64         { return 1 }
func (v boolValue) String() string           { return "bool" }
func (v boolValue) ToFloat() float64         { return 1 }
func (v boolValue) ToBoolean() bool          { return bool(v) }
func (v boolValue) Export() interface{}      { return v }
func (v boolValue) ExportType() reflect.Type { return nil }

type mockScript struct {
	mock.Mock
}

func (m *mockScript) ExecuteOnSubject(ctx heimdall.Context, sub *subject.Subject) (script.Result, error) {
	args := m.Called(ctx, sub)

	if val := args.Get(0); val != nil {
		// nolint: forcetypeassert
		return val.(script.Result), nil
	}

	return nil, args.Error(1)
}

func (m *mockScript) ExecuteOnPayload(ctx heimdall.Context, payload any) (script.Result, error) {
	args := m.Called(ctx, payload)

	if val := args.Get(0); val != nil {
		// nolint: forcetypeassert
		return val.(script.Result), nil
	}

	return nil, args.Error(1)
}
