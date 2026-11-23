package contacts

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/outbound"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	minBatchSize = 1
	maxBatchSize = 50

	minPhoneDigits = 8
	maxPhoneDigits = 15
)

type InstanceFinder interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
}

type RecipientChecker interface {
	CheckRecipient(ctx context.Context, instanceID uuid.UUID, phone string) (outbound.Registration, error)
}

type CheckResult struct {
	Input  string  `json:"input"`
	Exists bool    `json:"exists"`
	JID    *string `json:"jid"`
	Phone  *string `json:"phone"`
}

type Service struct {
	instances InstanceFinder
	checker   RecipientChecker
}

func NewService(instances InstanceFinder, checker RecipientChecker) *Service {
	return &Service{instances: instances, checker: checker}
}

func (s *Service) Check(ctx context.Context, clientID string, instanceID uuid.UUID, phones []string) ([]CheckResult, error) {
	if len(phones) < minBatchSize || len(phones) > maxBatchSize {
		return nil, domain.BadRequest("phones must have 1 to 50 items")
	}
	if err := validatePhones(phones); err != nil {
		return nil, err
	}
	instance, err := s.instances.FindByID(ctx, clientID, instanceID)
	if err != nil {
		return nil, err
	}
	if instance.Status != domain.InstanceStatusConnected {
		return nil, domain.InstanceNotConnected()
	}
	results := make([]CheckResult, 0, len(phones))
	for _, input := range phones {
		result, err := s.checkOne(ctx, instance.ID, input)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func validatePhones(phones []string) error {
	for _, phone := range phones {
		if !validPhone(phone) {
			return domain.BadRequest("phone must be digits with country code")
		}
	}
	return nil
}

func validPhone(phone string) bool {
	if len(phone) < minPhoneDigits || len(phone) > maxPhoneDigits {
		return false
	}
	for _, character := range phone {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (s *Service) checkOne(ctx context.Context, instanceID uuid.UUID, input string) (CheckResult, error) {
	registration, err := s.checker.CheckRecipient(ctx, instanceID, input)
	if err != nil {
		return CheckResult{}, err
	}
	result := CheckResult{Input: input}
	if !registration.Exists {
		return result, nil
	}
	phone, _, _ := strings.Cut(registration.JID, "@")
	phone, _, _ = strings.Cut(phone, ":")
	jid := registration.JID
	result.Exists = true
	result.JID = &jid
	result.Phone = &phone
	return result, nil
}
