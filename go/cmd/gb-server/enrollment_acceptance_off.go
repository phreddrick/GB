//go:build !gb_enrollment_acceptance

package main

import (
	"github.com/phreddrick/GB/go/internal/db"
	"log/slog"
)

// Ordinary production builds have no enrollment crash-control implementation.
func configureEnrollmentAcceptance(_ *db.Store, _ *slog.Logger) {}
