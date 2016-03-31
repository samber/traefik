package provider

import (
	log "github.com/Sirupsen/logrus"
	"github.com/containous/traefik/types"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/restclient"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"reflect"
)

// Kubernetes holds configurations of the Kubernetes provider.
type Kubernetes struct {
	BaseProvider `mapstructure:",squash"`
	Endpoint     string
	Domain       string
}

// Provide allows the provider to provide configurations to traefik
// using the given configuration channel.
func (provider *Kubernetes) Provide(configurationChan chan<- types.ConfigMessage) error {
	//var KubernetesFuncMap = template.FuncMap{}
	var ingClient client.IngressInterface
	config := &restclient.Config{
		Host: provider.Endpoint,
	}
	kubeClient, err := client.New(config)
	if err != nil {
		log.Errorf("Failed to create kubernetes client: %v.", err)
		return err
	} else {
		ingClient = kubeClient.Extensions().Ingress(api.NamespaceAll)
	}
	// rateLimiter := util.NewTokenBucketRateLimiter(0.1, 1)

	go func() {
		known := &extensions.IngressList{}
		// Controller loop
		for {
			// rateLimiter.Accept()
			ingresses, err := ingClient.List(api.ListOptions{})
			if err != nil {
				log.Printf("Error retrieving ingresses: %v", err)
				continue
			}
			if reflect.DeepEqual(ingresses.Items, known.Items) {
				continue
			}
			templateObjects := types.Configuration{
				map[string]*types.Backend{},
				map[string]*types.Frontend{},
			}
			// Load up the pods for the service in this ingress.
			for _, i := range ingresses.Items {
				// Build a our listeners based on the ingress rules.
				for _, r := range i.Spec.Rules {
					for _, pa := range r.HTTP.Paths {
						if _, exists := templateObjects.Backends[r.Host+pa.Path]; !exists {
							templateObjects.Backends[r.Host+pa.Path] = &types.Backend{
								Servers: make(map[string]types.Server),
							}
						}
						if _, exists := templateObjects.Frontends[r.Host+pa.Path]; !exists {
							templateObjects.Frontends[r.Host+pa.Path] = &types.Frontend{
								Backend: r.Host + pa.Path,
								Routes:  make(map[string]types.Route),
							}
						}
						if _, exists := templateObjects.Frontends[r.Host+pa.Path].Routes[r.Host]; !exists {
							templateObjects.Frontends[r.Host+pa.Path].Routes[r.Host] = types.Route{
								Rule:  "Host",
								Value: r.Host,
							}
						}
						if len(pa.Path) > 0 {
							templateObjects.Frontends[r.Host+pa.Path].Routes[pa.Path] = types.Route{
								Rule:  "Path",
								Value: pa.Path,
							}
						}
						s, err := kubeClient.Services(i.ObjectMeta.Namespace).Get(pa.Backend.ServiceName)
						if err != nil {
							log.Printf("Error retrieving service: %v", err)
							continue
						}
						log.Debugf("Kubernetes services retrieved %#v", s)

						// Now we load all the pods for this service.
						ps, err := kubeClient.Pods(i.ObjectMeta.Namespace).List(api.ListOptions{
							LabelSelector: labels.SelectorFromSet(labels.Set(s.Spec.Selector)),
							FieldSelector: fields.Everything(),
						})
						if err != nil {
							log.Printf("Error retrieving service: %v", err)
							continue
						}
						log.Debugf("Kubernetes pods retrieved %#v", ps)
						for _, pod := range ps.Items {
							templateObjects.Backends[r.Host+pa.Path].Servers[string(pod.UID)] = types.Server{
								URL:    "http://" + pod.Status.PodIP + ":" + pa.Backend.ServicePort.String(),
								Weight: 1,
							}
						}
					}
				}
			}
			configurationChan <- types.ConfigMessage{
				ProviderName:  "kubernetes",
				Configuration: &templateObjects,
			}
		}
	}()
	return nil
}
