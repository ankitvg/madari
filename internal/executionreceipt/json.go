package executionreceipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Marshal validates and emits one deterministic, indented V1 JSON document
// followed by a newline.
func Marshal(receipt Receipt) ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, fmt.Errorf("validate execution receipt: %w", err)
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal execution receipt: %w", err)
	}
	return append(payload, '\n'), nil
}

// Parse strictly decodes and validates one V1 JSON document.
func Parse(payload []byte) (Receipt, error) {
	if strings.TrimSpace(string(payload)) == "" {
		return Receipt{}, fmt.Errorf("execution receipt payload is empty")
	}

	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("parse execution receipt JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Receipt{}, fmt.Errorf("parse execution receipt JSON: trailing data after receipt document")
		}
		return Receipt{}, fmt.Errorf("parse execution receipt JSON: trailing data: %w", err)
	}
	if err := validateRequiredFieldPresence(payload); err != nil {
		return Receipt{}, fmt.Errorf("parse execution receipt JSON: %w", err)
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, fmt.Errorf("validate execution receipt: %w", err)
	}
	return receipt, nil
}

func validateRequiredFieldPresence(payload []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return err
	}
	if err := requireExactFields("receipt", root,
		"schema_version", "evidence", "run_id", "producer", "target", "started_at", "finished_at",
		"duration_ms", "phase", "outcome", "reason_code", "artifact", "client", "rings", "servers",
		"skills", "authority", "forwarded_environment", "effective_timeout_ns", "process_started",
		"termination", "exit"); err != nil {
		return err
	}
	if err := requireNonNullFields("receipt", root,
		"schema_version", "evidence", "run_id", "producer", "target", "started_at", "finished_at",
		"duration_ms", "phase", "outcome", "reason_code", "rings", "servers", "skills", "authority",
		"forwarded_environment", "process_started"); err != nil {
		return err
	}
	if err := requireNestedNonNullObject("evidence", root["evidence"], "kind", "cryptographic_attestation"); err != nil {
		return err
	}
	if err := requireNestedNonNullObject("producer", root["producer"], "name", "version"); err != nil {
		return err
	}
	if !isJSONNull(root["artifact"]) {
		if err := requireNestedNonNullObject("artifact", root["artifact"], "launch_digest", "policy_digest"); err != nil {
			return err
		}
	}
	if !isJSONNull(root["client"]) {
		if err := requireNestedNonNullObject("client", root["client"], "name", "version"); err != nil {
			return err
		}
	}
	for _, field := range []string{"rings", "servers", "skills"} {
		if err := requireObjectArrayFields(field, root[field], "name", "sha256"); err != nil {
			return err
		}
		if err := requireObjectArrayNonNullFields(field, root[field], "name"); err != nil {
			return err
		}
	}

	var authority map[string]json.RawMessage
	if err := json.Unmarshal(root["authority"], &authority); err != nil {
		return fmt.Errorf("authority must be an object: %w", err)
	}
	if err := requireExactFields("authority", authority, "requested", "effective"); err != nil {
		return err
	}
	if err := requireNonNullFields("authority", authority, "requested", "effective"); err != nil {
		return err
	}
	for _, field := range []string{"requested", "effective"} {
		if err := requireObjectArrayFields("authority."+field, authority[field], "control", "enforced_by", "verification", "classification"); err != nil {
			return err
		}
		if err := requireObjectArrayNonNullFields("authority."+field, authority[field], "control", "enforced_by", "verification", "classification"); err != nil {
			return err
		}
	}

	var forwarding []json.RawMessage
	if err := json.Unmarshal(root["forwarded_environment"], &forwarding); err != nil {
		return fmt.Errorf("forwarded_environment must be an array: %w", err)
	}
	for i, raw := range forwarding {
		label := fmt.Sprintf("forwarded_environment[%d]", i)
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("%s must be an object: %w", label, err)
		}
		if err := requireExactFields(label, entry, "recipient", "keys"); err != nil {
			return err
		}
		if err := requireNonNullFields(label, entry, "recipient", "keys"); err != nil {
			return err
		}
		if err := requireNestedNonNullObject(label+".recipient", entry["recipient"], "kind", "name"); err != nil {
			return err
		}
	}

	if !isJSONNull(root["termination"]) {
		if err := requireNestedNonNullObject("termination", root["termination"], "reason", "tree_termination"); err != nil {
			return err
		}
	}
	if !isJSONNull(root["exit"]) {
		if err := requireNestedObject("exit", root["exit"], "code", "signal"); err != nil {
			return err
		}
	}
	return nil
}

func requireNestedObject(label string, raw json.RawMessage, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("%s must be an object: %w", label, err)
	}
	return requireExactFields(label, object, fields...)
}

func requireNestedNonNullObject(label string, raw json.RawMessage, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("%s must be an object: %w", label, err)
	}
	if err := requireExactFields(label, object, fields...); err != nil {
		return err
	}
	return requireNonNullFields(label, object, fields...)
}

func requireObjectArrayFields(label string, raw json.RawMessage, fields ...string) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("%s must be an array: %w", label, err)
	}
	for i, entry := range entries {
		if err := requireNestedObject(fmt.Sprintf("%s[%d]", label, i), entry, fields...); err != nil {
			return err
		}
	}
	return nil
}

func requireObjectArrayNonNullFields(label string, raw json.RawMessage, fields ...string) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("%s must be an array: %w", label, err)
	}
	for i, entry := range entries {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry, &object); err != nil {
			return fmt.Errorf("%s[%d] must be an object: %w", label, i, err)
		}
		if err := requireNonNullFields(fmt.Sprintf("%s[%d]", label, i), object, fields...); err != nil {
			return err
		}
	}
	return nil
}

func requireExactFields(label string, object map[string]json.RawMessage, fields ...string) error {
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
		if _, ok := object[field]; !ok {
			return fmt.Errorf("%s is missing required field %q", label, field)
		}
	}
	for field := range object {
		if _, ok := wanted[field]; !ok {
			return fmt.Errorf("%s contains unknown field %q", label, field)
		}
	}
	return nil
}

func requireNonNullFields(label string, object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		if isJSONNull(object[field]) {
			return fmt.Errorf("%s field %q must not be null", label, field)
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
