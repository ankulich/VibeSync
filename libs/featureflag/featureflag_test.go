package featureflag

import (
	"context"
	"testing"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

func TestMissingFlagReturnsOff(t *testing.T) {
	t.Parallel()
	e := New(NewMemoryStore())
	v, err := e.Evaluate(context.Background(), "nope", Context{UserID: "u1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != VariantOff {
		t.Errorf("missing flag = %v, want off", v)
	}
}

func TestEnvironmentDefault(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(Flag{
		Key: "f",
		Defaults: map[string]Variant{
			"default": VariantOff,
			"prod":    VariantOn,
		},
	})
	e := New(store)

	if v, _ := e.Evaluate(context.Background(), "f", Context{Environment: "prod"}); v != VariantOn {
		t.Error("prod should resolve to on")
	}
	if v, _ := e.Evaluate(context.Background(), "f", Context{Environment: "dev"}); v != VariantOff {
		t.Error("unknown env should fall back to default=off")
	}
}

func TestUserOverrideBeatsRolloutAndDefault(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(Flag{
		Key:            "f",
		RolloutPercent: 100, // would serve "on" to everyone
		RolloutVariant: VariantOn,
		Defaults:       map[string]Variant{"default": VariantOff},
		UserOverrides:  map[string]Variant{"u-pinned": VariantOff},
	})
	e := New(store)

	if v, _ := e.Evaluate(context.Background(), "f", Context{UserID: "anyone"}); v != VariantOn {
		t.Error("non-pinned user should get rollout=on")
	}
	if v, _ := e.Evaluate(context.Background(), "f", Context{UserID: "u-pinned"}); v != VariantOff {
		t.Error("pinned user override must win over rollout")
	}
}

func TestPercentageBucketingIsStable(t *testing.T) {
	t.Parallel()
	// Same user+flag always buckets the same way.
	b1 := bucket("flag", "user-123")
	b2 := bucket("flag", "user-123")
	if b1 != b2 {
		t.Errorf("bucketing not stable: %d vs %d", b1, b2)
	}
}

func TestRBACImportsGenGo(t *testing.T) {
	// Sanity that the gen/go module resolves from this module via go.work.
	if commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR.String() == "" {
		t.Fatal("expected non-empty enum name")
	}
}
