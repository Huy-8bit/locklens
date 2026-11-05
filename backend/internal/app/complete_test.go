package app

import (
	"strings"
	"testing"

	"github.com/locklens/locklens/internal/domain/notify"
	"github.com/locklens/locklens/internal/repository"
)

func TestCompleteFindsAForgottenRepository(t *testing.T) {
	var store *repository.Store
	if err := complete(notify.Repositories{NotificationRepository: store}); err == nil || !strings.Contains(err.Error(), "ChangeRepository") {
		t.Fatalf("missing repository: %v", err)
	}
}
