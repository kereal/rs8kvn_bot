package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== Source Lifecycle Tests ====================

func TestListSources_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	sources, err := svc.ListSources(context.Background())
	assert.NoError(t, err)
	// NewService seeds one default source
	assert.Len(t, sources, 1)
	assert.Equal(t, "default", sources[0].Name)
}

func TestSeedDefaultSource_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.SeedDefaultSource(ctx, "main", "http://xui:2053", "token-abc", 1, "https://sub.example.com")
	require.NoError(t, err)

	sources, err := svc.ListSources(ctx)
	require.NoError(t, err)
	require.Len(t, sources, 2)

	var mainSrc *Source
	for i := range sources {
		if sources[i].Name == "main" {
			mainSrc = &sources[i]
			break
		}
	}
	require.NotNil(t, mainSrc, "main source must exist after SeedDefaultSource")
	assert.Equal(t, "http://xui:2053", mainSrc.XUIHost)
	assert.Equal(t, "token-abc", mainSrc.XUIAPIToken)
	assert.Equal(t, 1, mainSrc.XUIInboundID)
	assert.Equal(t, "https://sub.example.com", mainSrc.SubURL)
	assert.True(t, mainSrc.Active)
}

func TestIsSourcesEmpty_TrueAndFalse(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// NewService seeds a default source, so not empty
	empty, err := svc.IsSourcesEmpty(ctx)
	require.NoError(t, err)
	assert.False(t, empty, "default source should be present after NewService")
}

// ==================== Plan Tests ====================

func TestGetPlanByName_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	plan, err := svc.GetPlanByName(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, plan)
}

func TestGetPlanByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	plan, err := svc.GetPlanByID(context.Background(), 9999)
	assert.Error(t, err)
	assert.Nil(t, plan)
}

func TestGetPlanByID_AfterCreate(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{
		Name:         "pro",
		Price:        9.99,
		DevicesLimit: 3,
		TrafficLimit: 50 * 1024 * 1024 * 1024,
		Duration:     720,
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	require.NotZero(t, plan.ID)

	got, err := svc.GetPlanByID(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Name)
	assert.Equal(t, 9.99, got.Price)
	assert.Equal(t, 3, got.DevicesLimit)
}

func TestGetPlanByName_AfterCreate(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "vip", Price: 19.99, DevicesLimit: 5, TrafficLimit: 100 * 1024 * 1024 * 1024}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)

	got, err := svc.GetPlanByName(ctx, "vip")
	require.NoError(t, err)
	assert.Equal(t, plan.ID, got.ID)
	assert.Equal(t, "vip", got.Name)
}

func TestGetPlanByName_DefaultTrialPlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	got, err := svc.GetPlanByName(context.Background(), TrialPlanName)
	require.NoError(t, err)
	assert.Equal(t, TrialPlanName, got.Name)
	assert.Equal(t, 1, got.DevicesLimit)
	assert.Equal(t, 3, got.Duration)
}

func TestGetPlanByName_DefaultFreePlan(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	got, err := svc.GetPlanByName(context.Background(), FreePlanName)
	require.NoError(t, err)
	assert.Equal(t, FreePlanName, got.Name)
}

// ==================== GetSourcesByPlanName Tests ====================

func TestGetSourcesByPlanName_NoLinks(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	sources, err := svc.GetSourcesByPlanName(context.Background(), "nonexistent_plan")
	assert.NoError(t, err)
	assert.Empty(t, sources)

	// Seed migration 012 links the default source to both 'trial' and 'free' plans,
	// so a real plan name returns the default source rather than empty.
	trialSources, err := svc.GetSourcesByPlanName(context.Background(), TrialPlanName)
	assert.NoError(t, err)
	assert.Len(t, trialSources, 1, "seed links default source to trial plan")
}

func TestGetSourcesByPlanName_ReturnsLinkedSources(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	var trialPlan Plan
	require.NoError(t, svc.db.WithContext(ctx).Where("name = ?", TrialPlanName).First(&trialPlan).Error)

	// Seed migration 012 already links the default source to the trial plan,
	// so we only need to add a second source and link it.
	require.NoError(t, svc.SeedDefaultSource(ctx, "backup", "http://x2", "t2", 1, ""))
	allSources, err := svc.ListSources(ctx)
	require.NoError(t, err)
	for _, src := range allSources {
		if src.Name == "backup" {
			require.NoError(t, svc.db.WithContext(ctx).Create(&PlanSource{PlanID: trialPlan.ID, SourceID: src.ID}).Error)
		}
	}

	linked, err := svc.GetSourcesByPlanName(ctx, "trial")
	require.NoError(t, err)
	assert.Len(t, linked, 2)

	names := []string{linked[0].Name, linked[1].Name}
	assert.Contains(t, names, "default")
	assert.Contains(t, names, "backup")
}

func TestGetSourcesByPlanName_FilterByName(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Seed migration 012 links the default source to BOTH trial and free plans,
	// so both lookups should return that single source.
	trialSources, err := svc.GetSourcesByPlanName(ctx, "trial")
	require.NoError(t, err)
	assert.Len(t, trialSources, 1)
	assert.Equal(t, "default", trialSources[0].Name)

	freeSources, err := svc.GetSourcesByPlanName(ctx, "free")
	require.NoError(t, err)
	assert.Len(t, freeSources, 1)
	assert.Equal(t, "default", freeSources[0].Name)
}

// ==================== Subscription Active Check (model) ====================

func TestSubscription_IsActive_StatusCases(t *testing.T) {
	t.Parallel()

	now := time.Now()
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	tests := []struct {
		name       string
		status     string
		expiryTime time.Time
		want       bool
	}{
		{"active + future expiry", "active", future, true},
		{"active + past expiry", "active", past, false},
		{"active + zero expiry", "active", time.Time{}, true},
		{"revoked + future expiry", "revoked", future, false},
		{"expired + future expiry", "expired", future, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{Status: tt.status, ExpiryTime: tt.expiryTime}
			assert.Equal(t, tt.want, sub.IsActive())
		})
	}
}
