package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func currentNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("empty request body")
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func podDisplayName(pod *corev1.Pod) string {
	if pod.Name != "" {
		return pod.Name
	}
	return pod.GenerateName + "<pending-name>"
}

// jsonPatchEscape escapes '~' and '/' per RFC 6901, needed because map keys
// used as JSON Patch path segments must not contain raw '/' or '~'.
func jsonPatchEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func ownerMatches(ref, owner metav1.OwnerReference) bool {
	if ref.UID != "" && owner.UID != "" {
		return ref.UID == owner.UID
	}
	return ref.APIVersion == owner.APIVersion && ref.Kind == owner.Kind && ref.Name == owner.Name
}
