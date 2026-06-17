package database

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// ListNodes returns all configured nodes.
func (s *Service) ListNodes(ctx context.Context) ([]Node, error) {
	var nodes []Node
	result := s.db.WithContext(ctx).Find(&nodes)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", result.Error)
	}
	return nodes, nil
}

// GetNodesByPlanName returns nodes for the plan with the given name.
func (s *Service) GetNodesByPlanName(ctx context.Context, planName string) ([]Node, error) {
	var nodes []Node
	result := s.db.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Joins("JOIN plans ON plans.id = plan_nodes.plan_id").
		Where("plans.name = ?", planName).
		Find(&nodes)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get nodes by plan name: %w", result.Error)
	}
	return nodes, nil
}

// GetPlanByName returns a plan by its name.
func (s *Service) GetPlanByName(ctx context.Context, name string) (*Plan, error) {
	var plan Plan
	result := s.db.WithContext(ctx).Where("name = ?", name).First(&plan)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get plan by name: %w", result.Error)
	}
	return &plan, nil
}

// GetPlanByID returns a plan by its ID.
func (s *Service) GetPlanByID(ctx context.Context, id uint) (*Plan, error) {
	var plan Plan
	result := s.db.WithContext(ctx).First(&plan, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get plan by id: %w", result.Error)
	}
	return &plan, nil
}

// IsNodesEmpty returns true if no nodes exist in the database.
func (s *Service) IsNodesEmpty(ctx context.Context) (bool, error) {
	var count int64
	result := s.db.WithContext(ctx).Model(&Node{}).Count(&count)
	if result.Error != nil {
		return false, fmt.Errorf("failed to count nodes: %w", result.Error)
	}
	return count == 0, nil
}

// SeedDefaultNode inserts the default node from environment variables if the nodes table is empty.
// It also links all existing plans to the new node and assigns the free plan to legacy subscriptions.
func (s *Service) SeedDefaultNode(ctx context.Context, name, host, apiToken string, inboundIDs []int, subscriptionURL string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node := Node{
			Name:            name,
			IsActive:        true,
			Host:            host,
			APIToken:        apiToken,
			SubscriptionURL: subscriptionURL,
			Type:            "x-ui",
		}
		if err := node.SetInboundIDs(inboundIDs); err != nil {
			return err
		}
		if err := tx.Create(&node).Error; err != nil {
			return err
		}
		var plans []Plan
		if err := tx.Find(&plans).Error; err != nil {
			return err
		}
		for _, p := range plans {
			pn := PlanNode{PlanID: p.ID, NodeID: node.ID}
			if err := tx.Create(&pn).Error; err != nil {
				return fmt.Errorf("failed to link plan %d to node %d: %w", p.ID, node.ID, err)
			}
		}
		return tx.Exec(
			`UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE name = ?) WHERE plan_id IS NULL`,
			FreePlanName,
		).Error
	})
}

// LinkNodeToPlan creates a link between a node and a plan (plan_nodes entry).
func (s *Service) LinkNodeToPlan(ctx context.Context, planName string, nodeID uint) error {
	var plan Plan
	if err := s.db.WithContext(ctx).Where("name = ?", planName).First(&plan).Error; err != nil {
		return fmt.Errorf("plan %q not found: %w", planName, err)
	}
	return s.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: nodeID}).Error
}
