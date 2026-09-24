// PODTetris mutating admission webhook
// Intercepts Pod CREATE requests, finds a matching open PodMove, pins the pod to PodMove.spec.targetNode via spec.nodeName,
// and marks a condition so the recreation is recorded on the PodMove (LastTransitionTime is the recreation timestamp).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ADDRESS                           = ":8443"
	conditionReasonReplacementCreated = "ReplacementCreated"
)

var (
	log *zap.Logger

	// podMoveCache serves PodMove reads from an informer in podtetrisNamespace.
	podMoveCache cache.Cache
	// apiClient is the live API client for Pod reads and PodMove claim writes.
	apiClient client.Client

	podtetrisNamespace = "podtetris"
	codecs             = serializer.NewCodecFactory(runtime.NewScheme())
	deserializer       = codecs.UniversalDeserializer()
)

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()
	log = logger.Named("webhook")
	ctrl.SetLogger(zapr.NewLogger(logger))

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal("Could not load in-cluster config", zap.Error(err))
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	if err := podtetrisiov1.AddToScheme(scheme); err != nil {
		log.Fatal("Could not register PodMove scheme", zap.Error(err))
	}

	if ns, err := currentNamespace(); err != nil {
		log.Info("Cannot determine current pod namespace; using default",
			zap.Error(err),
			zap.String("namespace", podtetrisNamespace),
		)
	} else {
		podtetrisNamespace = ns
	}

	podMoveCache, err = cache.New(cfg, cache.Options{
		Scheme:                      scheme,
		DefaultNamespaces:           map[string]cache.Config{podtetrisNamespace: {}},
		ReaderFailOnMissingInformer: true,
	})
	if err != nil {
		log.Fatal("Could not create PodMove cache", zap.Error(err))
	}
	apiClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatal("Could not create Kubernetes client", zap.Error(err))
	}

	ctx := context.Background()
	if _, err := podMoveCache.GetInformer(ctx, &podtetrisiov1.PodMove{}); err != nil {
		log.Fatal("Could not create PodMove informer", zap.Error(err))
	}
	go func() {
		if err := podMoveCache.Start(ctx); err != nil {
			log.Fatal("PodMove cache stopped", zap.Error(err))
		}
	}()
	if !podMoveCache.WaitForCacheSync(ctx) {
		log.Fatal("Timed out waiting for PodMove cache sync")
	}

	log.Info("PODTetris webhook starting", zap.String("namespace", podtetrisNamespace))

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

	log.Info("Listening", zap.String("address", ADDRESS))
	if err := server.ListenAndServeTLS("/etc/webhook/certs/tls.crt", "/etc/webhook/certs/tls.key"); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Webhook server failed", zap.Error(err))
	}
}
