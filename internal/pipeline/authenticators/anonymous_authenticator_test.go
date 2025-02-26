package authenticators

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/heimdall/mocks"
	"github.com/dadrus/heimdall/internal/testsupport"
)

func TestCreateAnonymousAuthenticator(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc     string
		id     string
		config []byte
		assert func(t *testing.T, err error, auth *anonymousAuthenticator)
	}{
		{
			uc:     "subject is set to anon",
			id:     "auth1",
			config: []byte("subject: anon"),
			assert: func(t *testing.T, err error, auth *anonymousAuthenticator) {
				t.Helper()

				require.NoError(t, err)

				assert.Equal(t, "anon", auth.Subject)
				assert.Equal(t, "auth1", auth.HandlerID())
			},
		},
		{
			uc:     "default subject",
			id:     "auth1",
			config: nil,
			assert: func(t *testing.T, err error, auth *anonymousAuthenticator) {
				t.Helper()

				require.NoError(t, err)

				assert.Equal(t, "anonymous", auth.Subject)
				assert.Equal(t, "auth1", auth.HandlerID())
			},
		},
		{
			uc:     "unsupported attributes",
			id:     "auth1",
			config: []byte("foo: bar"),
			assert: func(t *testing.T, err error, auth *anonymousAuthenticator) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to decode")
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			// WHEN
			auth, err := newAnonymousAuthenticator(tc.id, conf)

			// THEN
			tc.assert(t, err, auth)
		})
	}
}

func TestCreateAnonymousAuthenticatorFromPrototype(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc              string
		id              string
		prototypeConfig []byte
		config          []byte
		assert          func(t *testing.T, err error, prototype *anonymousAuthenticator, configured *anonymousAuthenticator)
	}{
		{
			uc: "no new configuration for the configured authenticator",
			id: "auth2",
			assert: func(t *testing.T, err error, prototype *anonymousAuthenticator, configured *anonymousAuthenticator) {
				t.Helper()

				require.NoError(t, err)

				assert.Equal(t, prototype, configured)
				assert.Equal(t, "anonymous", configured.Subject)
				assert.Equal(t, "auth2", configured.HandlerID())
			},
		},
		{
			uc:              "new subject for the configured authenticator",
			id:              "auth2",
			prototypeConfig: []byte("subject: anon"),
			config:          []byte("subject: foo"),
			assert: func(t *testing.T, err error, prototype *anonymousAuthenticator, configured *anonymousAuthenticator) {
				t.Helper()

				require.NoError(t, err)

				assert.NotEqual(t, prototype, configured)
				assert.Equal(t, prototype.id, configured.id)
				assert.Equal(t, "auth2", configured.HandlerID())
				assert.NotEqual(t, prototype.Subject, configured.Subject)
				assert.Equal(t, "anon", prototype.Subject)
				assert.Equal(t, "foo", configured.Subject)
			},
		},
		{
			uc:     "malformed configured authenticator config",
			id:     "auth2",
			config: []byte("foo: bar"),
			assert: func(t *testing.T, err error, prototype *anonymousAuthenticator, configured *anonymousAuthenticator) {
				t.Helper()

				assert.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to decode")
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			pc, err := testsupport.DecodeTestConfig(tc.prototypeConfig)
			require.NoError(t, err)

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			prototype, err := newAnonymousAuthenticator(tc.id, pc)
			require.NoError(t, err)

			// WHEN
			auth, err := prototype.WithConfig(conf)

			// THEN
			baa, ok := auth.(*anonymousAuthenticator)
			require.True(t, ok)

			tc.assert(t, err, prototype, baa)
		})
	}
}

func TestAnonymousAuthenticatorExecute(t *testing.T) {
	t.Parallel()

	// GIVEN
	subjectID := "anon"
	auth := anonymousAuthenticator{Subject: subjectID, id: "anon_auth"}

	ctx := &mocks.MockContext{}
	ctx.On("AppContext").Return(context.Background())

	// WHEN
	sub, err := auth.Execute(ctx)

	// THEN
	require.NoError(t, err)
	assert.NotNil(t, sub)
	assert.Equal(t, subjectID, sub.ID)
	assert.Empty(t, sub.Attributes)
	ctx.AssertExpectations(t)
}

func TestAnonymousAuthenticatorIsFallbackOnErrorAllowed(t *testing.T) {
	t.Parallel()

	// GIVEN
	auth := anonymousAuthenticator{Subject: "foo"}

	// WHEN
	isAllowed := auth.IsFallbackOnErrorAllowed()

	// THEN
	require.False(t, isAllowed)
}
