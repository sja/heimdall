package authorizers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/heimdall/mocks"
	"github.com/dadrus/heimdall/internal/pipeline/subject"
	"github.com/dadrus/heimdall/internal/testsupport"
)

func TestCreateLocalAuthorizer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc     string
		id     string
		config []byte
		assert func(t *testing.T, err error, auth *localAuthorizer)
	}{
		{
			uc: "without configuration",
			assert: func(t *testing.T, err error, auth *localAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no script provided")
			},
		},
		{
			uc:     "without script",
			config: []byte(``),
			assert: func(t *testing.T, err error, auth *localAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no script provided")
			},
		},
		{
			uc:     "with malformed script",
			config: []byte(`script: "return foo"`),
			assert: func(t *testing.T, err error, auth *localAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to compile")
			},
		},
		{
			uc: "with unsupported attributes",
			config: []byte(`
script: "return foo"
foo: bar
`),
			assert: func(t *testing.T, err error, auth *localAuthorizer) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc:     "with valid script",
			id:     "authz",
			config: []byte(`script: "console.log('Executing JS Code')"`),
			assert: func(t *testing.T, err error, auth *localAuthorizer) {
				t.Helper()

				require.NoError(t, err)
				assert.NotNil(t, auth.s)
				assert.Equal(t, "authz", auth.HandlerID())
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			// WHEN
			a, err := newLocalAuthorizer(tc.id, conf)

			// THEN
			tc.assert(t, err, a)
		})
	}
}

func TestCreateLocalAuthorizerFromPrototype(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc              string
		id              string
		prototypeConfig []byte
		config          []byte
		assert          func(t *testing.T, err error, prototype *localAuthorizer, configured *localAuthorizer)
	}{
		{
			uc:              "no new configuration provided",
			prototypeConfig: []byte(`script: "console.log('Executing JS Code')"`),
			assert: func(t *testing.T, err error, prototype *localAuthorizer, configured *localAuthorizer) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, prototype, configured)
			},
		},
		{
			uc:              "configuration without script provided",
			prototypeConfig: []byte(`script: "console.log('Executing JS Code')"`),
			config:          []byte(``),
			assert: func(t *testing.T, err error, prototype *localAuthorizer, configured *localAuthorizer) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, prototype, configured)
			},
		},
		{
			uc:              "new script provided",
			id:              "authz",
			prototypeConfig: []byte(`script: "console.log('Executing JS Code')"`),
			config:          []byte(`script: "console.log('New JS script')"`),
			assert: func(t *testing.T, err error, prototype *localAuthorizer, configured *localAuthorizer) {
				t.Helper()

				require.NoError(t, err)
				assert.NotEqual(t, prototype, configured)
				require.NotNil(t, configured)
				assert.NotEqual(t, prototype.s, configured.s)
				assert.Equal(t, "authz", configured.HandlerID())
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			pc, err := testsupport.DecodeTestConfig(tc.prototypeConfig)
			require.NoError(t, err)

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			prototype, err := newLocalAuthorizer(tc.id, pc)
			require.NoError(t, err)

			// WHEN
			auth, err := prototype.WithConfig(conf)

			// THEN
			locAuth, ok := auth.(*localAuthorizer)
			require.True(t, ok)

			tc.assert(t, err, prototype, locAuth)
		})
	}
}

func TestLocalAuthorizerExecute(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc                         string
		id                         string
		config                     []byte
		configureContextAndSubject func(t *testing.T, ctx *mocks.MockContext, sub *subject.Subject)
		assert                     func(t *testing.T, err error)
	}{
		{
			uc:     "denied by script using throw",
			id:     "authz1",
			config: []byte(`script: "throw('denied by script')"`),
			configureContextAndSubject: func(t *testing.T, ctx *mocks.MockContext, sub *subject.Subject) {
				// nothing is required here
				t.Helper()
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrAuthorization)
				assert.Contains(t, err.Error(), "denied by script")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz1", identifier.HandlerID())
			},
		},
		{
			uc:     "denied by script using boolean value",
			id:     "authz1",
			config: []byte(`script: "false"`),
			configureContextAndSubject: func(t *testing.T, ctx *mocks.MockContext, sub *subject.Subject) {
				// nothing is required here
				t.Helper()
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrAuthorization)
				assert.Contains(t, err.Error(), "script returned false")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz1", identifier.HandlerID())
			},
		},
		{
			uc:     "script can use subject and context",
			id:     "authz2",
			config: []byte(`script: "throw(heimdall.RequestHeader(heimdall.Subject.ID))"`),
			configureContextAndSubject: func(t *testing.T, ctx *mocks.MockContext, sub *subject.Subject) {
				t.Helper()

				sub.ID = "foobar"
				ctx.On("RequestHeader", "foobar").Return("barfoo")
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrAuthorization)
				assert.Contains(t, err.Error(), "barfoo")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "authz2", identifier.HandlerID())
			},
		},
		{
			uc:     "allowed by script",
			config: []byte(`script: "true"`),
			configureContextAndSubject: func(t *testing.T, ctx *mocks.MockContext, sub *subject.Subject) {
				// nothing is required here
				t.Helper()
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				require.NoError(t, err)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			mctx := &mocks.MockContext{}
			mctx.On("AppContext").Return(context.Background())

			sub := &subject.Subject{}

			tc.configureContextAndSubject(t, mctx, sub)

			auth, err := newLocalAuthorizer(tc.id, conf)
			require.NoError(t, err)

			// WHEN
			err = auth.Execute(mctx, sub)

			// THEN
			tc.assert(t, err)

			mctx.AssertExpectations(t)
		})
	}
}
