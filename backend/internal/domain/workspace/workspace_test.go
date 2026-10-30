package workspace

import (
	"testing"
)

func TestCancelRequiresSessionAndTarget(t *testing.T) {
	called := false
	s := New(Repositories{}, nil).(*service)
	s.active["request"] = running{"owner", 3, func() { called = true }}
	if s.Cancel("other", 3, "request") || s.Cancel("owner", 4, "request") || called {
		t.Fatal("cancel escaped ownership")
	}
	if !s.Cancel("owner", 3, "request") || !called {
		t.Fatal("owner cannot cancel")
	}
}
