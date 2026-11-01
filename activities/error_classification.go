package activities

import (
	"context"
	"errors"
	"strings"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"

	"github.com/datagill/datagill-core/shared"
)

// NonRetryableServiceErrorType is the Temporal error type for a shared.NonRetryableServiceError.
const NonRetryableServiceErrorType = "NonRetryableServiceError"

// ClassifyActivityError makes an activity error's retry intent visible to Temporal.
//
// Temporal only reads retry settings (non-retryable, next retry delay) from the outermost
// error an activity returns, and only when it is a *temporal.ApplicationError. Anything else
// is retried by the activity's retry policy, which defaults to forever. So:
//   - a *temporal.ApplicationError wrapped by fmt.Errorf is brought back to the top, keeping
//     its type, non-retryable flag and next retry delay (e.g. a GitHub rate limit);
//   - a shared.NonRetryableServiceError anywhere in the chain becomes a non-retryable
//     ApplicationError (e.g. 401 bad credentials, 404 missing installation);
//   - everything else is returned unchanged and stays retryable.
func ClassifyActivityError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*temporal.ApplicationError); ok { //nolint:errorlint // only the outermost error matters to Temporal
		return err
	}

	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		// Keep the wrapping context ("EnrichPullRequest get token: ...") without repeating
		// the inner error's type and cause, which the new error adds back.
		msg := strings.Replace(err.Error(), appErr.Error(), appErr.Message(), 1)
		return temporal.NewApplicationErrorWithOptions(msg, appErr.Type(), temporal.ApplicationErrorOptions{ //nolint:wrapcheck // must be outermost for Temporal
			NonRetryable:   appErr.NonRetryable(),
			NextRetryDelay: appErr.NextRetryDelay(),
			Category:       appErr.Category(),
			Cause:          appErr.Unwrap(),
		})
	}

	var nonRetryable *shared.NonRetryableServiceError
	if errors.As(err, &nonRetryable) {
		// No cause: err.Error() already includes it.
		return temporal.NewNonRetryableApplicationError(err.Error(), NonRetryableServiceErrorType, nil) //nolint:wrapcheck // must be outermost for Temporal
	}

	return err
}

// ErrorClassificationInterceptor applies ClassifyActivityError to every activity a worker
// runs, so individual activities can return plain wrapped errors.
type ErrorClassificationInterceptor struct {
	interceptor.WorkerInterceptorBase
}

// InterceptActivity implements interceptor.WorkerInterceptor.
func (*ErrorClassificationInterceptor) InterceptActivity(_ context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	i := &errorClassificationActivityInterceptor{}
	i.Next = next
	return i
}

type errorClassificationActivityInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
}

func (i *errorClassificationActivityInterceptor) ExecuteActivity(ctx context.Context, in *interceptor.ExecuteActivityInput) (any, error) {
	result, err := i.Next.ExecuteActivity(ctx, in)
	return result, ClassifyActivityError(err)
}
