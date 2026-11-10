//go:build gb_enrollment_acceptance

package main

// Compiled exclusively into the disposable subprocess acceptance binary.
// There is no network control. The parent waits for a bounded stage marker and
// sends SIGKILL; neither credentials, request values nor world models are logged.
import (
	"github.com/phreddrick/GB/go/internal/db"
	"log/slog"
	"os"
)

func configureEnrollmentAcceptance(store *db.Store, logger *slog.Logger) {
	stage := os.Getenv("GB_ACCEPTANCE_ENROLL_STAGE")
	switch stage {
	case "enrollment.before", "enrollment.construct", "enrollment.race", "enrollment.before-commit", "enrollment.after-commit":
	default:
		return
	}
	store.SetMetaFault(func(current string) error {
		if current == stage {
			logger.Warn("CR07F enrollment barrier", slog.String("stage", stage))
			select {}
		}
		return nil
	})
}
