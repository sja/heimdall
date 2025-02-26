package httpendpoint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/dadrus/heimdall/internal/cache"
	"github.com/dadrus/heimdall/internal/cache/mocks"
	"github.com/dadrus/heimdall/internal/endpoint"
	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/x"
	otelmock "github.com/dadrus/heimdall/internal/x/opentelemetry/mocks"
)

func TestRuleSetEndpointInit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc     string
		ep     *ruleSetEndpoint
		assert func(t *testing.T, err error, ep *ruleSetEndpoint)
	}{
		{
			uc: "init fails due to not set url",
			ep: &ruleSetEndpoint{},
			assert: func(t *testing.T, err error, ep *ruleSetEndpoint) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "validation")
			},
		},
		{
			uc: "init successful",
			ep: &ruleSetEndpoint{Endpoint: endpoint.Endpoint{URL: "http://foo.bar"}},
			assert: func(t *testing.T, err error, ep *ruleSetEndpoint) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, "http://foo.bar", ep.URL)
				assert.Equal(t, http.MethodGet, ep.Method)
				require.NotNil(t, ep.HTTPCacheEnabled)
				assert.True(t, *ep.HTTPCacheEnabled)
			},
		},
	} {
		t.Run(tc.uc, func(t *testing.T) {
			// WHEN
			err := tc.ep.init()

			// THEN
			tc.assert(t, err, tc.ep)
		})
	}
}

func TestRuleSetEndpointFetchRuleSet(t *testing.T) { //nolint:maintidx
	t.Parallel()

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	otel.SetTracerProvider(otelmock.NewMockTraceProvider())

	type ResponseWriter func(t *testing.T, w http.ResponseWriter)

	var writeResponse ResponseWriter

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotEmpty(t, r.Header.Get("Traceparent"))

		writeResponse(t, w)
	}))

	defer srv.Close()

	for _, tc := range []struct {
		uc            string
		ep            *ruleSetEndpoint
		writeResponse ResponseWriter
		assert        func(t *testing.T, err error, ruleSet *RuleSet)
	}{
		{
			uc: "rule set loading error due to DNS error",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    "https://foo.bar.local/rules.yaml",
					Method: http.MethodGet,
				},
			},
			assert: func(t *testing.T, err error, _ *RuleSet) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrCommunication)
				assert.Contains(t, err.Error(), "endpoint failed")
			},
		},
		{
			uc: "rule set loading error due to server error response",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.WriteHeader(http.StatusBadRequest)
			},
			assert: func(t *testing.T, err error, _ *RuleSet) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrCommunication)
				assert.Contains(t, err.Error(), "response code: 400")
			},
		},
		{
			uc: "rule set loading error due to not set Content-Type for a not empty body",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				_, err := w.Write([]byte("foobar"))
				require.NoError(t, err)
			},
			assert: func(t *testing.T, err error, _ *RuleSet) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrInternal)
				assert.Contains(t, err.Error(), "content type")
			},
		},
		{
			uc: "empty rule set is returned on response with empty body",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.WriteHeader(http.StatusOK)
			},
			assert: func(t *testing.T, err error, ruleSet *RuleSet) {
				t.Helper()

				require.NoError(t, err)

				require.NotNil(t, ruleSet)
				require.Empty(t, ruleSet.Rules)
				require.NotEmpty(t, ruleSet.Hash)
			},
		},
		{
			uc: "valid rule set without path prefix from yaml",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.Header().Set("Content-Type", "application/yaml")
				_, err := w.Write([]byte("- id: foo"))
				require.NoError(t, err)
			},
			assert: func(t *testing.T, err error, ruleSet *RuleSet) {
				t.Helper()

				require.NoError(t, err)

				require.NotNil(t, ruleSet)
				assert.Len(t, ruleSet.Rules, 1)
				assert.Equal(t, "foo", ruleSet.Rules[0].ID)
				require.NotEmpty(t, ruleSet.Hash)
			},
		},
		{
			uc: "valid rule set without path prefix from json",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`[{"id":"foo"}]`))
				require.NoError(t, err)
			},
			assert: func(t *testing.T, err error, ruleSet *RuleSet) {
				t.Helper()

				require.NoError(t, err)

				require.NotNil(t, ruleSet)
				assert.Len(t, ruleSet.Rules, 1)
				assert.Equal(t, "foo", ruleSet.Rules[0].ID)
				require.NotEmpty(t, ruleSet.Hash)
			},
		},
		{
			uc: "valid rule set with path only url glob with path prefix violation",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
				RulesPathPrefix: "/foo/bar",
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`[{"id":"foo", "url":"/bar/foo/<**>"}]`))
				require.NoError(t, err)
			},
			assert: func(t *testing.T, err error, _ *RuleSet) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "path prefix validation")
			},
		},
		{
			uc: "valid rule set with full url glob with path prefix violation",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
				RulesPathPrefix: "/foo/bar",
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`[{"id":"foo", "url":"<**>://moobar.local:9090/bar/foo/<**>"}]`))
				require.NoError(t, err)
			},
			assert: func(t *testing.T, err error, _ *RuleSet) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "path prefix validation")
			},
		},
		{
			uc: "valid rule set with full url glob without path prefix violation",
			ep: &ruleSetEndpoint{
				Endpoint: endpoint.Endpoint{
					URL:    srv.URL,
					Method: http.MethodGet,
				},
				RulesPathPrefix: "/foo/bar",
			},
			writeResponse: func(t *testing.T, w http.ResponseWriter) {
				t.Helper()

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`[{"id":"foo", "url":"<**>://moobar.local:9090/foo/bar/<**>"}]`))
				require.NoError(t, err)
			},
			assert: func(t *testing.T, err error, ruleSet *RuleSet) {
				t.Helper()

				require.NoError(t, err)

				require.NotNil(t, ruleSet)
				assert.Len(t, ruleSet.Rules, 1)
				assert.Equal(t, "foo", ruleSet.Rules[0].ID)
				require.NotEmpty(t, ruleSet.Hash)
			},
		},
	} {
		t.Run(tc.uc, func(t *testing.T) {
			// GIVEN
			cch := &mocks.MockCache{}
			ctx := log.Logger.With().
				Str("_rule_provider_type", "http_endpoint").
				Logger().
				WithContext(cache.WithContext(context.Background(), cch))

			writeResponse = x.IfThenElse(tc.writeResponse != nil,
				tc.writeResponse,
				func(t *testing.T, w http.ResponseWriter) {
					t.Helper()

					w.WriteHeader(http.StatusOK)
				})

			// WHEN
			ruleSet, err := tc.ep.FetchRuleSet(ctx)

			// THEN
			tc.assert(t, err, &ruleSet)
		})
	}
}
