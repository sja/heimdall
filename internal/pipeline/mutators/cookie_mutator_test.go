package mutators

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
	"github.com/dadrus/heimdall/internal/x"
)

func TestCreateCookieMutator(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc     string
		id     string
		config []byte
		assert func(t *testing.T, err error, mut *cookieMutator)
	}{
		{
			uc: "without configuration",
			assert: func(t *testing.T, err error, mut *cookieMutator) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no cookie")
			},
		},
		{
			uc:     "without cookie configuration",
			config: []byte(``),
			assert: func(t *testing.T, err error, mut *cookieMutator) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "no cookie")
			},
		},
		{
			uc: "with unsupported attributes",
			config: []byte(`
cookies:
  foo: bar
foo: bar
`),
			assert: func(t *testing.T, err error, mut *cookieMutator) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc: "with bad template",
			config: []byte(`
cookies:
  bar: "{{ .Subject.ID | foobar }}"
`),
			assert: func(t *testing.T, err error, mut *cookieMutator) {
				t.Helper()

				require.Nil(t, mut)
				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrConfiguration)
				assert.Contains(t, err.Error(), "failed to unmarshal")
			},
		},
		{
			uc: "with valid config",
			id: "cmut",
			config: []byte(`
cookies:
  foo: bar
  bar: "{{ .Subject.ID }}"`),
			assert: func(t *testing.T, err error, mut *cookieMutator) {
				t.Helper()

				require.NoError(t, err)
				assert.Len(t, mut.cookies, 2)
				assert.Equal(t, "cmut", mut.HandlerID())

				val, err := mut.cookies["foo"].Render(nil, nil)
				require.NoError(t, err)
				assert.Equal(t, "bar", val)

				val, err = mut.cookies["bar"].Render(nil, &subject.Subject{ID: "baz"})
				require.NoError(t, err)
				assert.Equal(t, "baz", val)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			// WHEN
			mutator, err := newCookieMutator(tc.id, conf)

			// THEN
			tc.assert(t, err, mutator)
		})
	}
}

func TestCreateCookieMutatorFromPrototype(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc              string
		id              string
		prototypeConfig []byte
		config          []byte
		assert          func(t *testing.T, err error, prototype *cookieMutator, configured *cookieMutator)
	}{
		{
			uc: "no new configuration provided",
			id: "cmut1",
			prototypeConfig: []byte(`
cookies:
  foo: bar
`),
			assert: func(t *testing.T, err error, prototype *cookieMutator, configured *cookieMutator) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, prototype, configured)
				assert.Equal(t, "cmut1", configured.HandlerID())
			},
		},
		{
			uc: "configuration without cookies provided",
			id: "cmut2",
			prototypeConfig: []byte(`
cookies:
  foo: bar
`),
			config: []byte(``),
			assert: func(t *testing.T, err error, prototype *cookieMutator, configured *cookieMutator) {
				t.Helper()

				require.NoError(t, err)
				assert.Equal(t, prototype, configured)
				assert.Equal(t, "cmut2", configured.HandlerID())
			},
		},
		{
			uc: "new cookies provided",
			id: "cmut3",
			prototypeConfig: []byte(`
cookies:
  foo: bar
`),
			config: []byte(`
cookies:
  bar: foo
`),
			assert: func(t *testing.T, err error, prototype *cookieMutator, configured *cookieMutator) {
				t.Helper()

				require.NoError(t, err)
				assert.NotEqual(t, prototype, configured)
				require.NotNil(t, configured)
				assert.NotEmpty(t, configured.cookies)
				assert.Equal(t, "cmut3", configured.HandlerID())

				val, err := configured.cookies["bar"].Render(nil, nil)
				require.NoError(t, err)
				assert.Equal(t, "foo", val)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			pc, err := testsupport.DecodeTestConfig(tc.prototypeConfig)
			require.NoError(t, err)

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			prototype, err := newCookieMutator(tc.id, pc)
			require.NoError(t, err)

			// WHEN
			mut, err := prototype.WithConfig(conf)

			// THEN
			cookieMut, ok := mut.(*cookieMutator)
			require.True(t, ok)

			tc.assert(t, err, prototype, cookieMut)
		})
	}
}

func TestCookieMutatorExecute(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc               string
		id               string
		config           []byte
		configureContext func(t *testing.T, ctx *mocks.MockContext)
		createSubject    func(t *testing.T) *subject.Subject
		assert           func(t *testing.T, err error)
	}{
		{
			uc: "with nil subject",
			id: "cmut1",
			config: []byte(`
cookies:
  foo: bar
  bar: "{{ .Subject.ID }}"
`),
			assert: func(t *testing.T, err error) {
				t.Helper()

				require.Error(t, err)
				assert.ErrorIs(t, err, heimdall.ErrInternal)
				assert.Contains(t, err.Error(), "'nil' subject")

				var identifier interface{ HandlerID() string }
				require.True(t, errors.As(err, &identifier))
				assert.Equal(t, "cmut1", identifier.HandlerID())
			},
		},
		{
			uc: "with all preconditions satisfied",
			config: []byte(`
cookies:
  foo: "{{ .Subject.Attributes.bar }}"
  bar: "{{ .Subject.ID }}"
  baz: bar
`),
			configureContext: func(t *testing.T, ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("AddCookieForUpstream", "foo", "baz")
				ctx.On("AddCookieForUpstream", "bar", "FooBar")
				ctx.On("AddCookieForUpstream", "baz", "bar")
			},
			createSubject: func(t *testing.T) *subject.Subject {
				t.Helper()

				return &subject.Subject{ID: "FooBar", Attributes: map[string]any{"bar": "baz"}}
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				require.NoError(t, err)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			createSubject := x.IfThenElse(tc.createSubject != nil,
				tc.createSubject,
				func(t *testing.T) *subject.Subject {
					t.Helper()

					return nil
				})

			configureContext := x.IfThenElse(tc.configureContext != nil,
				tc.configureContext,
				func(t *testing.T, ctx *mocks.MockContext) { t.Helper() })

			conf, err := testsupport.DecodeTestConfig(tc.config)
			require.NoError(t, err)

			mctx := &mocks.MockContext{}
			mctx.On("AppContext").Return(context.Background())

			sub := createSubject(t)

			configureContext(t, mctx)

			mutator, err := newCookieMutator(tc.id, conf)
			require.NoError(t, err)

			// WHEN
			err = mutator.Execute(mctx, sub)

			// THEN
			tc.assert(t, err)

			mctx.AssertExpectations(t)
		})
	}
}
