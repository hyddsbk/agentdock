package browser

import "fmt"

const (
	ErrNotFound        = "BROWSER_NOT_FOUND"
	ErrLaunchFailed    = "LAUNCH_FAILED"
	ErrProfileInUse    = "PROFILE_IN_USE"
	ErrSessionNotFound = "SESSION_NOT_FOUND"
	ErrPageNotFound    = "PAGE_NOT_FOUND"
	ErrActionInvalid   = "ACTION_INVALID"
	ErrActionFailed    = "ACTION_FAILED"
	ErrTimeout         = "TIMEOUT"
	ErrCDPFailed       = "CDP_FAILED"
	ErrCDPAmbiguous    = "CDP_AMBIGUOUS"
	ErrPolicyDenied    = "SECURITY_POLICY_DENIED"
)

// ErrorDetails is deliberately typed so dynamic maps never cross the browser
// service boundary. The app protocol layer converts it to MCP/JSON output.
type ErrorDetails struct {
	Path             string    `json:"path,omitempty"`
	Browser          Kind      `json:"browser,omitempty"`
	ProfileID        string    `json:"profile_id,omitempty"`
	SessionID        string    `json:"session_id,omitempty"`
	PageID           string    `json:"page_id,omitempty"`
	AvailablePageIDs []string  `json:"available_page_ids,omitempty"`
	ActionIndex      *int      `json:"action_index,omitempty"`
	Action           string    `json:"action,omitempty"`
	WaitUntil        WaitUntil `json:"wait_until,omitempty"`
	Direction        int64     `json:"direction,omitempty"`
	URL              string    `json:"url,omitempty"`
	URLPattern       string    `json:"url_pattern,omitempty"`
	Method           string    `json:"method,omitempty"`
	Status           int       `json:"status,omitempty"`
	Field            string    `json:"field,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	Selector         string    `json:"selector,omitempty"`
	ItemIndex        *int      `json:"index,omitempty"`
	Count            int       `json:"count,omitempty"`
}

type Error struct {
	Code    string
	Message string
	Phase   string
	Details *ErrorDetails
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func browserError(code, message, phase string, details *ErrorDetails, cause error) *Error {
	return &Error{Code: code, Message: message, Phase: phase, Details: details, Cause: cause}
}
