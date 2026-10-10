package automation

// encatch: tests for ticket custom attribute conditions and the "missing attribute
// counts as empty" behaviour. Kept in a separate file from upstream's tests.

import (
	"encoding/json"
	"testing"

	"github.com/abhinavxd/libredesk/internal/automation/models"
	cmodels "github.com/abhinavxd/libredesk/internal/conversation/models"
)

func TestEncatchCustomAttributeConditions(t *testing.T) {
	e := createTestEngine(&mockConversationStore{})
	conv := createTestConversation(func(c *cmodels.Conversation) {
		c.CustomAttributes = json.RawMessage(`{"ticket_tier":"Enterprise Premium","org_id":42}`)
		c.Contact.CustomAttributes = json.RawMessage(`{}`)
	})
	noAttrs := createTestConversation(func(c *cmodels.Conversation) {
		c.CustomAttributes = nil
		c.Contact.CustomAttributes = nil
	})

	ticket := models.FieldTypeConversationCustomAttribute
	contact := models.FieldTypeContactCustomAttribute

	cases := []struct {
		name string
		conv cmodels.Conversation
		rule models.RuleDetail
		want bool
	}{
		{"ticket attribute equals", conv, models.RuleDetail{Field: "ticket_tier", FieldType: ticket, Operator: models.RuleOperatorEquals, Value: "Enterprise Premium"}, true},
		{"ticket attribute not equals", conv, models.RuleDetail{Field: "ticket_tier", FieldType: ticket, Operator: models.RuleOperatorEquals, Value: "SaaS Growth"}, false},
		{"ticket numeric attribute", conv, models.RuleDetail{Field: "org_id", FieldType: ticket, Operator: models.RuleOperatorEquals, Value: "42"}, true},
		{"ticket attribute set", conv, models.RuleDetail{Field: "ticket_tier", FieldType: ticket, Operator: models.RuleOperatorSet}, true},
		{"missing ticket attribute is not set", conv, models.RuleDetail{Field: "project_id", FieldType: ticket, Operator: models.RuleOperatorNotSet}, true},
		{"missing contact attribute is not set", conv, models.RuleDetail{Field: "plan", FieldType: contact, Operator: models.RuleOperatorNotSet}, true},
		{"missing contact attribute does not equal a value", conv, models.RuleDetail{Field: "plan", FieldType: contact, Operator: models.RuleOperatorEquals, Value: "SaaS Standard"}, false},
		{"nil attributes are not set", noAttrs, models.RuleDetail{Field: "ticket_tier", FieldType: ticket, Operator: models.RuleOperatorNotSet}, true},
		{"nil attributes do not match a value", noAttrs, models.RuleDetail{Field: "plan", FieldType: contact, Operator: models.RuleOperatorEquals, Value: "Growth Plus"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.evaluateRule(tc.rule, tc.conv, nil); got != tc.want {
				t.Fatalf("evaluateRule() = %v, want %v", got, tc.want)
			}
		})
	}
}
