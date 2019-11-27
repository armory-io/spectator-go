package spectator

import (
	"encoding/json"
	"net/http"
)

type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Value struct {
	V int   `json:"v"`
	T int64 `json:"t"`
}

type TopValue struct {
	Tags   []Tag    `json:"tags"`
	Values []*Value `json:"values"`
}

type Metric struct {
	Kind   string     `json:"kind"`
	Values []TopValue `json:"values"`
}

func HttpHandler(registry *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		payload := registry.GetExport()
		b, _ := json.Marshal(payload)
		w.Write(b)
	}
}
