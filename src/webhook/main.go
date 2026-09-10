// PODTetris mutating admission webhook
// Intercepts Pod CREATE requests, finds a matching open PodMove, pins the pod to PodMove.spec.targetNode via spec.nodeName,
// and marks a condition so the recreation is recorded on the PodMove (LastTransitionTime is the recreation timestamp).
package main

import (
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"time"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ADDRESS                           = ":8443"
	conditionReasonReplacementCreated = "ReplacementCreated"
)

var (
	k8sClient          client.Client
	podtetrisNamespace = "podtetris"
	codecs             = serializer.NewCodecFactory(runtime.NewScheme())
	deserializer       = codecs.UniversalDeserializer()
)

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("could not load in-cluster config: %v", err)
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	if err := podtetrisiov1.AddToScheme(scheme); err != nil {
		log.Fatalf("could not register PodMove scheme: %v", err)
	}
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("could not create Kubernetes client: %v", err)
	}

	if ns, err := currentNamespace(); err != nil {
		log.Printf("Cannot determine current pod namespace: %v; using %q", err, podtetrisNamespace)
	} else {
		podtetrisNamespace = ns
	}

	log.Printf("PODTetris webhook starting (namespace=%s)...", podtetrisNamespace)

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", handleMutate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:         ADDRESS,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
	}

	log.Printf("Listening on %s", ADDRESS)
	if err := server.ListenAndServeTLS("/etc/webhook/certs/tls.crt", "/etc/webhook/certs/tls.key"); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Webhook server failed: %v", err)
	}
}
