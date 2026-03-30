package runtime

import (
	"os"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

// NewKubernetesClient creates a controller-runtime client from in-cluster config.
func NewKubernetesClient() (client.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(karov1alpha1.AddToScheme(scheme))

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return client.New(config, client.Options{Scheme: scheme})
}

// GetEnvOrDefault returns the env var value or a default.
func GetEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
