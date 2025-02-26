package errorhandlers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/heimdall/mocks"
	"github.com/dadrus/heimdall/internal/testsupport"
	"github.com/dadrus/heimdall/internal/x"
)

func TestCreateWWWAuthenticateErrorHandler(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc     string
		config []byte
		assert func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler)
	}{
		{
			uc:     "configuration without required 'When' parameter",
			config: []byte(`realm: FooBar`),
			assert: func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no 'when' error handler")
			},
		},
		{
			uc: "without provided configuration",
			assert: func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no 'when' error handler")
			},
		},
		{
			uc:     "with empty configuration",
			config: []byte(``),
			assert: func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no 'when' error handler")
			},
		},
		{
			uc: "with configuration containing unsupported fields",
			config: []byte(`
realm: FooBar
when:
  - error:
      - type: authentication_error
foo: bar
`),
			assert: func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc: "with minimum required configuration",
			config: []byte(`
when:
  - error:
      - type: authentication_error
`),
			assert: func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler) {
				t.Helper()

				require.NoError(t, err)
				require.NotNil(t, errorHandler)
				assert.Equal(t, "Please authenticate", errorHandler.realm)
				require.Len(t, errorHandler.m, 1)
				assert.Nil(t, errorHandler.m[0].CIDR)
				assert.Nil(t, errorHandler.m[0].Headers)
				require.NotNil(t, errorHandler.m[0].Error)
				errorDescriptors := *errorHandler.m[0].Error
				assert.Len(t, errorDescriptors, 1)
				matchingErrors := errorDescriptors[0].Errors
				assert.Len(t, matchingErrors, 1)
				assert.Equal(t, heimdall.ErrAuthentication, matchingErrors[0])
			},
		},
		{
			uc: "with all possible attributes",
			config: []byte(`
realm: "What is your password"
when:
  - error:
      - type: precondition_error
`),
			assert: func(t *testing.T, err error, errorHandler *wwwAuthenticateErrorHandler) {
				t.Helper()

				require.NoError(t, err)
				require.NotNil(t, errorHandler)
				assert.Equal(t, "What is your password", errorHandler.realm)
				require.Len(t, errorHandler.m, 1)
				assert.Nil(t, errorHandler.m[0].CIDR)
				assert.Nil(t, errorHandler.m[0].Headers)
				require.NotNil(t, errorHandler.m[0].Error)
				errorDescriptors := *errorHandler.m[0].Error
				assert.Len(t, errorDescriptors, 1)
				matchingErrors := errorDescriptors[0].Errors
				assert.Len(t, matchingErrors, 1)
				assert.Equal(t, heimdall.ErrArgument, matchingErrors[0])
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			// WHEN
			errorHandler, err := newWWWAuthenticateErrorHandler(conf)

			// THEN
			tc.assert(t, err, errorHandler)
		})
	}
}

func TestCreateWWWAuthenticateErrorHandlerFromPrototype(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc              string
		prototypeConfig []byte
		config          []byte
		assert          func(t *testing.T, err error, prototype *wwwAuthenticateErrorHandler,
			configured *wwwAuthenticateErrorHandler)
	}{
		{
			uc: "no new configuration provided",
			prototypeConfig: []byte(`
when:
  - error:
      - type: authentication_error
`),
			assert: func(t *testing.T, err error, prototype *wwwAuthenticateErrorHandler,
				configured *wwwAuthenticateErrorHandler,
			) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, prototype, configured)
			},
		},
		{
			uc: "empty configuration provided",
			prototypeConfig: []byte(`
when:
  - error:
      - type: authentication_error
`),
			config: []byte(``),
			assert: func(t *testing.T, err error, prototype *wwwAuthenticateErrorHandler,
				configured *wwwAuthenticateErrorHandler,
			) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, prototype, configured)
			},
		},
		{
			uc: "unsupported fields provided",
			prototypeConfig: []byte(`
when:
  - error:
      - type: authentication_error
`),
			config: []byte(`to: http://foo.bar`),
			assert: func(t *testing.T, err error, prototype *wwwAuthenticateErrorHandler,
				configured *wwwAuthenticateErrorHandler,
			) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc: "with 'when' reconfigured",
			prototypeConfig: []byte(`
when:
  - error:
      - type: authentication_error
      - type: authorization_error
`),
			config: []byte(`
when:
  - error:
      - type: precondition_error
`),
			assert: func(t *testing.T, err error, prototype *wwwAuthenticateErrorHandler,
				configured *wwwAuthenticateErrorHandler,
			) {
				t.Helper()

				require.NoError(t, err)
				assert.NotEqual(t, prototype, configured)
				assert.NotNil(t, configured)
				assert.Equal(t, "Please authenticate", prototype.realm)
				assert.Equal(t, prototype.realm, configured.realm)
				assert.NotEqual(t, prototype.m, configured.m)
				assert.Len(t, configured.m, 1)
				assert.Nil(t, configured.m[0].CIDR)
				assert.Nil(t, configured.m[0].Headers)
				assert.NotNil(t, configured.m[0].Error)

				errorDescriptors := *configured.m[0].Error
				assert.Len(t, errorDescriptors, 1)
				matchingErrors := errorDescriptors[0].Errors
				assert.Len(t, matchingErrors, 1)
				assert.Equal(t, heimdall.ErrArgument, matchingErrors[0])
			},
		},
		{
			uc: "with 'realm' reconfigured",
			prototypeConfig: []byte(`
when:
  - error:
      - type: authentication_error
`),
			config: []byte(`realm: "You password please"`),
			assert: func(t *testing.T, err error, prototype *wwwAuthenticateErrorHandler,
				configured *wwwAuthenticateErrorHandler,
			) {
				t.Helper()

				require.NoError(t, err)
				assert.NotEqual(t, prototype, configured)
				assert.NotNil(t, configured)
				assert.NotEqual(t, prototype.realm, configured.realm)
				assert.Equal(t, "You password please", configured.realm)
				assert.Equal(t, prototype.m, configured.m)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			pc, err := testsupport.DecodeTestConfig(tc.prototypeConfig)
			require.NoError(t, err)

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			prototype, err := newWWWAuthenticateErrorHandler(pc)
			require.NoError(t, err)

			// WHEN
			errorHandler, err := prototype.WithConfig(conf)

			// THEN
			var (
				wwwAuthEH *wwwAuthenticateErrorHandler
				ok        bool
			)

			if err == nil {
				wwwAuthEH, ok = errorHandler.(*wwwAuthenticateErrorHandler)
				require.True(t, ok)
			}

			tc.assert(t, err, prototype, wwwAuthEH)
		})
	}
}

func TestWWWAuthenticateErrorHandlerExecute(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc               string
		config           []byte
		error            error
		configureContext func(t *testing.T, ctx *mocks.MockContext)
		assert           func(t *testing.T, wasResponsible bool, err error)
	}{
		{
			uc: "not responsible for error",
			config: []byte(`
when:
  - error:
      - type: authentication_error
`),
			error: heimdall.ErrInternal,
			assert: func(t *testing.T, wasResponsible bool, err error) {
				t.Helper()

				assert.NoError(t, err)
				assert.False(t, wasResponsible)
			},
		},
		{
			uc: "responsible for error with default realm",
			config: []byte(`
when:
  - error:
      - type: authentication_error
`),
			error: heimdall.ErrAuthentication,
			configureContext: func(t *testing.T, ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("SetPipelineError", heimdall.ErrAuthentication)
				ctx.On("AddHeaderForUpstream", "WWW-Authenticate",
					mock.MatchedBy(func(val string) bool {
						assert.True(t, strings.HasPrefix(val, "Basic "))
						realm := strings.TrimLeft(val, "Basic ")
						assert.Equal(t, "realm=Please authenticate", realm)

						return true
					}))
			},
			assert: func(t *testing.T, wasResponsible bool, err error) {
				t.Helper()

				assert.NoError(t, err)
				assert.True(t, wasResponsible)
			},
		},
		{
			uc: "responsible for error with custom realm",
			config: []byte(`
realm: "Your password please"
when:
  - error:
      - type: authentication_error
`),
			error: heimdall.ErrAuthentication,
			configureContext: func(t *testing.T, ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("SetPipelineError", heimdall.ErrAuthentication)
				ctx.On("AddHeaderForUpstream", "WWW-Authenticate",
					mock.MatchedBy(func(val string) bool {
						assert.True(t, strings.HasPrefix(val, "Basic "))
						realm := strings.TrimLeft(val, "Basic ")
						assert.Equal(t, "realm=Your password please", realm)

						return true
					}))
			},
			assert: func(t *testing.T, wasResponsible bool, err error) {
				t.Helper()

				assert.NoError(t, err)
				assert.True(t, wasResponsible)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			configureContext := x.IfThenElse(tc.configureContext != nil,
				tc.configureContext,
				func(t *testing.T, ctx *mocks.MockContext) { t.Helper() })

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			mctx := &mocks.MockContext{}
			mctx.On("AppContext").Return(context.Background())

			configureContext(t, mctx)

			errorHandler, err := newWWWAuthenticateErrorHandler(conf)
			require.NoError(t, err)

			// WHEN
			wasResponsible, err := errorHandler.Execute(mctx, tc.error)

			// THEN
			tc.assert(t, wasResponsible, err)

			mctx.AssertExpectations(t)
		})
	}
}
