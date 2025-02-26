package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/heimdall/mocks"
)

func TestErrorConditionMatcherMatch(t *testing.T) {
	t.Parallel()

	cidrMatcher, err := NewCIDRMatcher([]string{"192.168.1.0/24"})
	require.NoError(t, err)

	for _, tc := range []struct {
		uc       string
		matcher  ErrorConditionMatcher
		setupCtx func(ctx *mocks.MockContext)
		err      error
		matching bool
	}{
		{
			uc: "doesn't match on error only if other criteria are specified",
			matcher: ErrorConditionMatcher{
				Error: func() *ErrorMatcher {
					errMatcher := ErrorMatcher([]ErrorDescriptor{
						{Errors: []error{heimdall.ErrConfiguration}},
					})

					return &errMatcher
				}(),
				CIDR:    cidrMatcher,
				Headers: &HeaderMatcher{"foobar": {"bar", "foo"}},
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestHeaders").Return(map[string]string{
					"foobar": "barfoo",
				})
				ctx.On("RequestClientIPs").Return([]string{
					"192.168.10.2",
				})
			},
			err:      heimdall.ErrConfiguration,
			matching: false,
		},
		{
			uc: "doesn't match on ip only if other criteria are specified",
			matcher: ErrorConditionMatcher{
				Error: func() *ErrorMatcher {
					errMatcher := ErrorMatcher([]ErrorDescriptor{
						{Errors: []error{heimdall.ErrConfiguration}},
					})

					return &errMatcher
				}(),
				CIDR:    cidrMatcher,
				Headers: &HeaderMatcher{"foobar": {"bar", "foo"}},
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestHeaders").Return(map[string]string{
					"foobar": "barfoo",
				})
				ctx.On("RequestClientIPs").Return([]string{
					"192.168.1.2",
				})
			},
			err:      heimdall.ErrArgument,
			matching: false,
		},
		{
			uc: "doesn't match on header only if other criteria are specified",
			matcher: ErrorConditionMatcher{
				Error: func() *ErrorMatcher {
					errMatcher := ErrorMatcher([]ErrorDescriptor{
						{Errors: []error{heimdall.ErrConfiguration}},
					})

					return &errMatcher
				}(),
				CIDR:    cidrMatcher,
				Headers: &HeaderMatcher{"foobar": {"bar", "foo"}},
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestHeaders").Return(map[string]string{
					"foobar": "bar",
				})
				ctx.On("RequestClientIPs").Return([]string{
					"192.168.10.2",
				})
			},
			err:      heimdall.ErrArgument,
			matching: false,
		},
		{
			uc: "doesn't match at all",
			matcher: ErrorConditionMatcher{
				Error: func() *ErrorMatcher {
					errMatcher := ErrorMatcher([]ErrorDescriptor{
						{Errors: []error{heimdall.ErrConfiguration}},
					})

					return &errMatcher
				}(),
				CIDR:    cidrMatcher,
				Headers: &HeaderMatcher{"foobar": {"bar", "foo"}},
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestHeaders").Return(map[string]string{
					"foobar": "barfoo",
				})
				ctx.On("RequestClientIPs").Return([]string{
					"192.168.10.2",
				})
			},
			err:      heimdall.ErrArgument,
			matching: false,
		},
		{
			uc: "matches having all matchers defined",
			matcher: ErrorConditionMatcher{
				Error: func() *ErrorMatcher {
					errMatcher := ErrorMatcher([]ErrorDescriptor{
						{Errors: []error{heimdall.ErrConfiguration}},
					})

					return &errMatcher
				}(),
				CIDR:    cidrMatcher,
				Headers: &HeaderMatcher{"foobar": {"bar", "foo"}},
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestHeaders").Return(map[string]string{
					"foobar": "bar",
				})
				ctx.On("RequestClientIPs").Return([]string{
					"192.168.1.2",
				})
			},
			err:      heimdall.ErrConfiguration,
			matching: true,
		},
		{
			uc: "matches having only error matcher defined",
			matcher: ErrorConditionMatcher{
				Error: func() *ErrorMatcher {
					errMatcher := ErrorMatcher([]ErrorDescriptor{
						{Errors: []error{heimdall.ErrConfiguration}},
					})

					return &errMatcher
				}(),
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()
			},
			err:      heimdall.ErrConfiguration,
			matching: true,
		},
		{
			uc: "matches having only header matcher defined",
			matcher: ErrorConditionMatcher{
				Headers: &HeaderMatcher{"foobar": {"bar", "foo"}},
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestHeaders").Return(map[string]string{
					"foobar": "bar",
				})
			},
			err:      heimdall.ErrArgument,
			matching: true,
		},
		{
			uc: "matches having only cidr matcher defined",
			matcher: ErrorConditionMatcher{
				CIDR: cidrMatcher,
			},
			setupCtx: func(ctx *mocks.MockContext) {
				t.Helper()

				ctx.On("RequestClientIPs").Return([]string{
					"192.168.1.2",
				})
			},
			err:      heimdall.ErrConfiguration,
			matching: true,
		},
	} {
		t.Run("case="+tc.uc, func(t *testing.T) {
			// GIVEN
			ctx := &mocks.MockContext{}
			tc.setupCtx(ctx)

			// WHEN
			matched := tc.matcher.Match(ctx, tc.err)

			// THEN
			assert.Equal(t, tc.matching, matched)
			ctx.AssertExpectations(t)
		})
	}
}
