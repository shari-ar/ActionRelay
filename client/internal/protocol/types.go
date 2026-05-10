package protocol

const (
	RequestBatchProtocol = "actionrelay.request_batch.v1"
	ResultPackageProtocol = "actionrelay.result_package.v1"
)

type RequestBatch struct {
	Protocol string        `json:"protocol"`
	BatchID  string        `json:"batch_id"`
	SentAt   string        `json:"sent_at"`
	Client   ClientMeta    `json:"client"`
	Limits   BatchLimits   `json:"limits"`
	Requests []RequestItem `json:"requests"`
}

type ClientMeta struct {
	BatchIntervalMS int    `json:"batch_interval_ms"`
	RouteMode       string `json:"route_mode"`
}

type BatchLimits struct {
	MaxResponseBytesPerRequest int `json:"max_response_bytes_per_request"`
	RequestTimeoutMS           int `json:"request_timeout_ms"`
	WorkerConcurrency          int `json:"worker_concurrency"`
}

type RequestItem struct {
	RequestID string            `json:"request_id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      *BodyData         `json:"body"`
}

type BodyData struct {
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}

type ResultPackage struct {
	Protocol string          `json:"protocol"`
	BatchID  string          `json:"batch_id"`
	OK       bool            `json:"ok"`
	Results  []RequestResult `json:"results"`
}

type RequestResult struct {
	RequestID string        `json:"request_id"`
	OK        bool          `json:"ok"`
	Response  *HTTPResponse `json:"response"`
	Error     *ResultError  `json:"error"`
}

type HTTPResponse struct {
	Status   int               `json:"status"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     ResponseBody      `json:"body"`
	URL      string            `json:"url"`
	TimingMS int64             `json:"timing_ms"`
}

type ResponseBody struct {
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

type ResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
