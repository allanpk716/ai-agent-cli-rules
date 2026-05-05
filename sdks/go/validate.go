package agentsdk

import "fmt"

// ValidateEnvelope checks that an envelope conforms to the JSONL protocol rules:
// required fields populated, valid type, and field exclusion enforced.
func ValidateEnvelope(env Envelope) error {
	if env.Version == "" {
		return fmt.Errorf("envelope missing version")
	}
	if env.Tool == "" {
		return fmt.Errorf("envelope missing tool")
	}
	if env.Type == "" {
		return fmt.Errorf("envelope missing type")
	}
	if env.Timestamp == "" {
		return fmt.Errorf("envelope missing timestamp")
	}

	// Validate type is one of the known types.
	switch env.Type {
	case TypeResult, TypeError, TypeWarning, TypeProgress:
		// ok
	default:
		return fmt.Errorf("invalid envelope type: %q", env.Type)
	}

	// Field exclusion rules based on type.
	switch env.Type {
	case TypeResult:
		if env.Data == nil {
			return fmt.Errorf("result envelope must have data")
		}
		if env.ErrorCode != "" {
			return fmt.Errorf("result envelope must not have error_code")
		}
		if env.Percent != 0 {
			return fmt.Errorf("result envelope must not have percent")
		}
	case TypeError:
		if env.ErrorCode == "" {
			return fmt.Errorf("error envelope must have error_code")
		}
		if env.Message == "" {
			return fmt.Errorf("error envelope must have message")
		}
		if env.Data != nil {
			return fmt.Errorf("error envelope must not have data")
		}
		if env.Percent != 0 {
			return fmt.Errorf("error envelope must not have percent")
		}
	case TypeWarning:
		if env.Message == "" {
			return fmt.Errorf("warning envelope must have message")
		}
		if env.Data != nil {
			return fmt.Errorf("warning envelope must not have data")
		}
		if env.ErrorCode != "" {
			return fmt.Errorf("warning envelope must not have error_code")
		}
		if env.Percent != 0 {
			return fmt.Errorf("warning envelope must not have percent")
		}
	case TypeProgress:
		if env.Percent < 0 || env.Percent > 100 {
			return fmt.Errorf("progress envelope percent must be 0-100, got %d", env.Percent)
		}
		if env.Data != nil {
			return fmt.Errorf("progress envelope must not have data")
		}
		if env.ErrorCode != "" {
			return fmt.Errorf("progress envelope must not have error_code")
		}
	}

	return nil
}
