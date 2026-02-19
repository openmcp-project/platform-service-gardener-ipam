package v1alpha1

import (
	"encoding/json"
	"fmt"
)

const AppliedRulesAnnotationKey = "ipam." + GroupName + "/applied-injection-rules"

// AppliedRulesAnnotation is used to generate/parse the annotation which holds the information which CIDRs have been injected into a Cluster by which injection rule.
// The outer map's keys are the IDs of the applied CIDRInjection rules.
// The inner map's keys are the paths where CIDRs have been injected, and the values are the actual injected CIDRs.
type AppliedRulesAnnotation map[string]map[string]string

// ToAnnotation converts the AppliedRulesAnnotation into a string suitable for use as annotation value.
// This is usually done by serializing it as JSON.
func (a AppliedRulesAnnotation) ToAnnotation() (string, error) {
	data, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("error marshaling the applied rules annotation object to json: %w", err)
	}
	return string(data), nil
}

// FromAnnotation populates the AppliedRulesAnnotation from a string annotation value.
// This is usually done by deserializing it from JSON.
// Note that this overwrites any existing data in the AppliedRulesAnnotation.
func (a AppliedRulesAnnotation) FromAnnotation(ann string) error {
	clear(a)
	if err := json.Unmarshal([]byte(ann), &a); err != nil {
		return fmt.Errorf("error unmarshaling the applied rules annotation json string: %w", err)
	}
	return nil
}
