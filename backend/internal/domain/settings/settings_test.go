package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

// memStore keeps settings as JSON in memory.
type memStore struct {
	repository.SettingRepository
	values map[string][]byte
}

func (m *memStore) GetSetting(_ context.Context, key string, dst any) error {
	raw, ok := m.values[key]
	if !ok {
		return repository.ErrNotFound
	}
	return json.Unmarshal(raw, dst)
}

func (m *memStore) PutSetting(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	m.values[key] = raw
	return err
}

func TestChangesAreAnnouncedButReloadsAreNot(t *testing.T) {
	ctx := context.Background()
	st := &memStore{values: map[string][]byte{}}
	changed := 0
	s, err := Load(ctx, Repositories{SettingRepository: st}, false, func() { changed++ })
	if err != nil || changed != 0 {
		t.Fatalf("load: %v, changed %d", err, changed)
	}
	th := rules.Defaults()
	th.LongTxWarnSeconds = 123
	if err := s.UpdateThresholds(ctx, th); err != nil || changed != 1 {
		t.Fatalf("update: %v, changed %d", err, changed)
	}
	// Another server changed the stored thresholds: a reload picks them up
	// without announcing a change again (servers would echo each other).
	th.LongTxWarnSeconds = 456
	_ = st.PutSetting(ctx, keyThresholds, th)
	if err := s.Reload(ctx); err != nil || changed != 1 || s.Thresholds().LongTxWarnSeconds != 456 {
		t.Fatalf("reload: %v, changed %d, %v", err, changed, s.Thresholds().LongTxWarnSeconds)
	}
}
