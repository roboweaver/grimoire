package config

import (
	"strings"
	"testing"
	"time"
)

// --- scheduler.trash_purge_interval_seconds (M23, Req 7.1-7.5) ---

const trashPurgeIntervalKey = "scheduler.trash_purge_interval_seconds"

func loadTrashPurgeInterval(t *testing.T, schedulerBlock string) (Config, error) {
	t.Helper()
	return Load(writeTempConfig(t, trashDaysBase+schedulerBlock))
}

func TestLoadTrashPurgeIntervalDefaultsTo3600(t *testing.T) {
	cfg, err := loadTrashPurgeInterval(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Scheduler.EffectiveTrashPurgeInterval(); got != time.Hour {
		t.Errorf("EffectiveTrashPurgeInterval() = %v, want default 1h", got)
	}
	if DefaultTrashPurgeIntervalSeconds != 3600 {
		t.Errorf("DefaultTrashPurgeIntervalSeconds = %d, want 3600", DefaultTrashPurgeIntervalSeconds)
	}
}

func TestLoadTrashPurgeIntervalExplicit(t *testing.T) {
	cfg, err := loadTrashPurgeInterval(t, "scheduler:\n  trash_purge_interval_seconds: 120\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Scheduler.TrashPurgeIntervalSeconds == nil || *cfg.Scheduler.TrashPurgeIntervalSeconds != 120 {
		t.Fatalf("TrashPurgeIntervalSeconds = %v, want pointer to 120", cfg.Scheduler.TrashPurgeIntervalSeconds)
	}
	if got := cfg.Scheduler.EffectiveTrashPurgeInterval(); got != 120*time.Second {
		t.Errorf("EffectiveTrashPurgeInterval() = %v, want 2m0s", got)
	}
}

func TestLoadTrashPurgeIntervalEnvOverridesFile(t *testing.T) {
	t.Setenv("GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS", "45")
	cfg, err := loadTrashPurgeInterval(t, "scheduler:\n  trash_purge_interval_seconds: 120\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Scheduler.EffectiveTrashPurgeInterval(); got != 45*time.Second {
		t.Errorf("EffectiveTrashPurgeInterval() = %v, want env override 45s", got)
	}
}

func TestLoadTrashPurgeIntervalEnvUnparsableIsIgnored(t *testing.T) {
	t.Setenv("GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS", "often")
	cfg, err := loadTrashPurgeInterval(t, "scheduler:\n  trash_purge_interval_seconds: 120\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Scheduler.EffectiveTrashPurgeInterval(); got != 120*time.Second {
		t.Errorf("EffectiveTrashPurgeInterval() = %v, want file value 2m0s when env is unparsable", got)
	}
}

func TestLoadTrashPurgeIntervalInvalidFileValuesFail(t *testing.T) {
	for _, v := range []string{"0", "-1", "7.5", `"30"`, "abc"} {
		v := v
		t.Run(v, func(t *testing.T) {
			_, err := loadTrashPurgeInterval(t, "scheduler:\n  trash_purge_interval_seconds: "+v+"\n")
			if err == nil {
				t.Fatalf("Load with trash_purge_interval_seconds %s: want error, got nil", v)
			}
			if !strings.Contains(err.Error(), trashPurgeIntervalKey) {
				t.Errorf("error %q does not name %s", err, trashPurgeIntervalKey)
			}
		})
	}
}

func TestLoadTrashPurgeIntervalNonPositiveEnvFails(t *testing.T) {
	for _, v := range []string{"0", "-5"} {
		v := v
		t.Run(v, func(t *testing.T) {
			t.Setenv("GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS", v)
			// File value is valid; the failure must come from the override.
			_, err := loadTrashPurgeInterval(t, "scheduler:\n  trash_purge_interval_seconds: 120\n")
			if err == nil {
				t.Fatalf("Load with env %s: want error, got nil", v)
			}
			if !strings.Contains(err.Error(), trashPurgeIntervalKey) {
				t.Errorf("error %q does not name %s", err, trashPurgeIntervalKey)
			}
		})
	}
}
