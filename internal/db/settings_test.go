package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

// [Requirement(DB-08)]
func TestSettings_UpsertReadListAndDelete(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	keyA := "test_setting_a"
	keyB := "test_setting_b"
	cleanKeys := func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM app_settings WHERE key = ANY($1)`, []string{keyA, keyB})
	}
	// Clear any leftovers from a previous run so the missing-key check is valid.
	cleanKeys()
	t.Cleanup(cleanKeys)

	// Missing key is not an error.
	val, ok, err := database.GetSetting(ctx, keyA)
	if err != nil {
		t.Fatalf("get missing setting: %v", err)
	}
	if ok || val != "" {
		t.Fatalf("expected missing setting, got ok=%v val=%q", ok, val)
	}

	// Insert then update.
	if err := database.SetSetting(ctx, keyA, "one"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	val, ok, err = database.GetSetting(ctx, keyA)
	if err != nil || !ok || val != "one" {
		t.Fatalf("expected one, got ok=%v val=%q err=%v", ok, val, err)
	}
	if err := database.SetSetting(ctx, keyA, "two"); err != nil {
		t.Fatalf("update setting: %v", err)
	}
	val, _, _ = database.GetSetting(ctx, keyA)
	if val != "two" {
		t.Fatalf("expected two after update, got %q", val)
	}

	// Batch write is atomic and visible.
	if err := database.SetSettings(ctx, map[string]string{keyA: "three", keyB: "bee"}); err != nil {
		t.Fatalf("set settings batch: %v", err)
	}
	all, err := database.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if all[keyA] != "three" || all[keyB] != "bee" {
		t.Fatalf("unexpected stored values: %v", all)
	}

	// Validation and nil-receiver behavior.
	if err := database.SetSetting(ctx, "", "x"); err == nil {
		t.Error("expected error for empty key")
	}
	if _, _, err := database.GetSetting(ctx, ""); err == nil {
		t.Error("expected error for empty key")
	}
	var nilDB *db.DB
	if _, _, err := nilDB.GetSetting(ctx, keyA); err == nil {
		t.Error("expected error for nil database")
	}
	if err := nilDB.SetSettings(ctx, map[string]string{keyA: "x"}); err == nil {
		t.Error("expected error for nil database")
	}
}
