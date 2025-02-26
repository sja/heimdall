package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/heimdall/mocks"
	"github.com/dadrus/heimdall/internal/pipeline/subject"
	rulemocks "github.com/dadrus/heimdall/internal/rules/mocks"
	"github.com/dadrus/heimdall/internal/testsupport"
)

func TestCompositeAuthenticatorExecution(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uc             string
		configureMocks func(t *testing.T, ctx heimdall.Context, first *rulemocks.MockSubjectCreator,
			second *rulemocks.MockSubjectCreator, sub *subject.Subject)
		assert func(t *testing.T, err error)
	}{
		{
			uc: "with fallback due to argument error on first authenticator",
			configureMocks: func(t *testing.T, ctx heimdall.Context, first *rulemocks.MockSubjectCreator,
				second *rulemocks.MockSubjectCreator, sub *subject.Subject,
			) {
				t.Helper()

				first.On("Execute", ctx).Return(nil, heimdall.ErrArgument)
				second.On("Execute", ctx).Return(sub, nil)
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				assert.NoError(t, err)
			},
		},
		{
			uc: "with fallback and both authenticators returning argument error",
			configureMocks: func(t *testing.T, ctx heimdall.Context, first *rulemocks.MockSubjectCreator,
				second *rulemocks.MockSubjectCreator, _ *subject.Subject,
			) {
				t.Helper()

				first.On("Execute", ctx).Return(nil, heimdall.ErrArgument)
				second.On("Execute", ctx).Return(nil, heimdall.ErrArgument)
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				assert.Error(t, err)
				assert.Equal(t, err, heimdall.ErrArgument)
			},
		},
		{
			uc: "without fallback as first authenticator returns an error not equal to argument error",
			configureMocks: func(t *testing.T, ctx heimdall.Context, first *rulemocks.MockSubjectCreator,
				second *rulemocks.MockSubjectCreator, _ *subject.Subject,
			) {
				t.Helper()

				first.On("Execute", ctx).Return(nil, testsupport.ErrTestPurpose)
				first.On("IsFallbackOnErrorAllowed").Return(false)
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				assert.Error(t, err)
				assert.Equal(t, err, testsupport.ErrTestPurpose)
			},
		},
		{
			uc: "with fallback on error since first authenticator allows fallback on any error",
			configureMocks: func(t *testing.T, ctx heimdall.Context, first *rulemocks.MockSubjectCreator,
				second *rulemocks.MockSubjectCreator, sub *subject.Subject,
			) {
				t.Helper()

				first.On("Execute", ctx).Return(nil, testsupport.ErrTestPurpose)
				first.On("IsFallbackOnErrorAllowed").Return(true)
				second.On("Execute", ctx).Return(sub, nil)
			},
			assert: func(t *testing.T, err error) {
				t.Helper()

				assert.NoError(t, err)
			},
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			sub := &subject.Subject{ID: "foo"}

			ctx := &mocks.MockContext{}
			ctx.On("AppContext").Return(context.Background())

			auth1 := &rulemocks.MockSubjectCreator{}
			auth2 := &rulemocks.MockSubjectCreator{}
			tc.configureMocks(t, ctx, auth1, auth2, sub)

			auth := compositeSubjectCreator{auth1, auth2}

			// WHEN
			rSub, err := auth.Execute(ctx)

			// THEN
			tc.assert(t, err)

			if err == nil {
				assert.Equal(t, sub, rSub)
			}

			auth1.AssertExpectations(t)
			auth2.AssertExpectations(t)
			ctx.AssertExpectations(t)
		})
	}
}
