// Package request answers the JSON requests that `--request` takes, which a
// recipe sends to the program that runs it to ask about its environment. Only
// the environment-variable request is answered.
package request

import (
	"encoding/json"
	"errors"
)

// request is the one request answered, read from its JSON.
type request struct {
	EnvironmentVariable *string `json:"environment-variable"`
}

// response is the answer, as upstream's JSON encodes it: null when the variable
// is not set, else its bytes under "Unix", as Unix gives them.
type response struct {
	EnvironmentVariable *osString `json:"environment-variable"`
}

// osString is an operating-system string, which for Unix is its bytes.
type osString struct {
	Unix []int `json:"Unix"`
}

// Answer returns the JSON response to the request in input, reading the
// environment through lookup. It is an error when input is not a request that
// is answered. Pure: lookup is the caller's.
func Answer(input string, lookup func(name string) (string, bool)) (string, error) {
	var req request
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", err
	}
	if req.EnvironmentVariable == nil {
		return "", errors.New("unsupported request")
	}
	var res response
	if value, ok := lookup(*req.EnvironmentVariable); ok {
		bytes := []byte(value)
		res.EnvironmentVariable = &osString{Unix: make([]int, len(bytes))}
		for i, b := range bytes {
			res.EnvironmentVariable.Unix[i] = int(b)
		}
	}
	out, err := json.Marshal(res)
	return string(out), err
}
