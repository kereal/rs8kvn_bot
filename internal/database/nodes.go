package database

import (
	"context"
	"errors"
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

// LinkNodeToPlan creates a link between a node and a plan (plan_nodes entry).
func (s *Service) LinkNodeToPlan(ctx context.Context, planName string, nodeID uint) error {
	var plan Plan
	if err := s.db.WithContext(ctx).Where("name = ?", planName).First(&plan).Error; err != nil {
		return fmt.Errorf("plan %q not found: %w", planName, err)
	}
	return s.db.WithContext(ctx).Create(&PlanNode{PlanID: plan.ID, NodeID: nodeID}).Error
}

// GetNodeByID retrieves a node by its ID.
func (s *Service) GetNodeByID(ctx context.Context, id uint) (*Node, error) {
	var node Node
	result := s.db.WithContext(ctx).First(&node, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("node not found: %w", ErrNodeNotFound)
		}
		return nil, fmt.Errorf("failed to get node: %w", result.Error)
	}
	return &node, nil
}

// ListEnabled returns all active nodes.
func (s *Service) ListEnabled(ctx context.Context) ([]Node, error) {
	var nodes []Node
	result := s.db.WithContext(ctx).Where("is_active = ?", true).Find(&nodes)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list enabled nodes: %w", result.Error)
	}
	return nodes, nil
}

// CreateNode inserts a new node and returns it with the assigned ID.
func (s *Service) CreateNode(ctx context.Context, node *Node) error {
	if err := s.db.WithContext(ctx).Create(node).Error; err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}
	return nil
}

// GetNodesByPlanID returns active nodes linked to the given plan via plan_nodes.
func (s *Service) GetNodesByPlanID(ctx context.Context, planID uint) ([]Node, error) {
	var nodes []Node
	result := s.db.WithContext(ctx).
		Table("nodes").
		Select("nodes.*").
		Joins("JOIN plan_nodes ON plan_nodes.node_id = nodes.id").
		Where("plan_nodes.plan_id = ? AND nodes.is_active = ?", planID, true).
		Find(&nodes)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get nodes by plan ID: %w", result.Error)
	}
	return nodes, nil
}
