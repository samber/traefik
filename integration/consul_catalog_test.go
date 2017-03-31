package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os/exec"
	"reflect"
	"time"

	"github.com/containous/traefik/types"
	"github.com/go-check/check"
	"github.com/hashicorp/consul/api"

	checker "github.com/vdemeester/shakers"
)

// Consul catalog test suites
type ConsulCatalogSuite struct {
	BaseSuite
	consulIP     string
	consulClient *api.Client
}

type service struct {
	name    string
	address string
	port    int
	tags    []string
}

func (s *ConsulCatalogSuite) SetUpSuite(c *check.C) {

	s.createComposeProject(c, "consul_catalog")
	s.composeProject.Start(c)

	consul := s.composeProject.Container(c, "consul")

	s.consulIP = consul.NetworkSettings.IPAddress
	config := api.DefaultConfig()
	config.Address = s.consulIP + ":8500"
	consulClient, err := api.NewClient(config)
	if err != nil {
		c.Fatalf("Error creating consul client")
	}
	s.consulClient = consulClient

	// Wait for consul to elect itself leader
	time.Sleep(2000 * time.Millisecond)
}

func (s *ConsulCatalogSuite) registerService(name string, address string, port int, tags []string) error {
	catalog := s.consulClient.Catalog()
	_, err := catalog.Register(
		&api.CatalogRegistration{
			Node:    address,
			Address: address,
			Service: &api.AgentService{
				ID:      name,
				Service: name,
				Address: address,
				Port:    port,
				Tags:    tags,
			},
		},
		&api.WriteOptions{},
	)
	return err
}

func (s *ConsulCatalogSuite) deregisterService(name string, address string) error {
	catalog := s.consulClient.Catalog()
	_, err := catalog.Deregister(
		&api.CatalogDeregistration{
			Node:      address,
			Address:   address,
			ServiceID: name,
		},
		&api.WriteOptions{},
	)
	return err
}

func (s *ConsulCatalogSuite) testE2eConfiguration(c *check.C, nodes []service) *types.Configuration {
	cmd := exec.Command(traefikBinary, "--consulCatalog", "--consulCatalog.endpoint="+s.consulIP+":8500", "--consulCatalog.domain=consul.localhost", "--configFile=fixtures/consul_catalog/simple.toml", "--web", "--web.address=:8080")
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	// registers some nodes and services
	for _, node := range nodes {
		err = s.registerService(node.name, node.address, node.port, node.tags)
		c.Assert(err, checker.IsNil)
	}

	time.Sleep(5000 * time.Millisecond)

	// retrieves traefik route table from api
	client := &http.Client{}
	req, err := http.NewRequest("GET", "http://127.0.0.1:8080/api/providers/consul_catalog", nil)
	c.Assert(err, checker.IsNil)
	req.Host = "localhost:8080"
	resp, err := client.Do(req)

	c.Assert(err, checker.IsNil)
	c.Assert(resp.StatusCode, checker.Equals, 200)

	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, checker.IsNil)

	var consulCatalogConfig types.Configuration
	json.Unmarshal(body, &consulCatalogConfig)
	return &consulCatalogConfig
}

func (s *ConsulCatalogSuite) TestSimpleConfiguration(c *check.C) {
	cmd := exec.Command(traefikBinary, "--consulCatalog", "--consulCatalog.endpoint="+s.consulIP+":8500", "--configFile=fixtures/consul_catalog/simple.toml")
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	time.Sleep(500 * time.Millisecond)
	// TODO validate : run on 80
	resp, err := http.Get("http://127.0.0.1:8000/")

	// Expected a 404 as we did not configure anything
	c.Assert(err, checker.IsNil)
	c.Assert(resp.StatusCode, checker.Equals, 404)
}

func (s *ConsulCatalogSuite) TestSingleService(c *check.C) {
	cmd := exec.Command(traefikBinary, "--consulCatalog", "--consulCatalog.endpoint="+s.consulIP+":8500", "--consulCatalog.domain=consul.localhost", "--configFile=fixtures/consul_catalog/simple.toml")
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	nginx := s.composeProject.Container(c, "nginx")

	err = s.registerService("test", nginx.NetworkSettings.IPAddress, 80, []string{})
	c.Assert(err, checker.IsNil, check.Commentf("Error registering service"))
	defer s.deregisterService("test", nginx.NetworkSettings.IPAddress)

	time.Sleep(5000 * time.Millisecond)
	client := &http.Client{}
	req, err := http.NewRequest("GET", "http://127.0.0.1:8000/", nil)
	c.Assert(err, checker.IsNil)
	req.Host = "test.consul.localhost"
	resp, err := client.Do(req)

	c.Assert(err, checker.IsNil)
	c.Assert(resp.StatusCode, checker.Equals, 200)

	_, err = ioutil.ReadAll(resp.Body)
	c.Assert(err, checker.IsNil)
}

func (s *ConsulCatalogSuite) TestSingleServiceMultipleRules(c *check.C) {
	defaultEntrypoints := []string{"http"}

	cases := []struct {
		nodes    []service
		expected *types.Configuration
	}{
		{
			nodes: []service{
				{
					name:    "foobar",
					address: "1.1.1.1",
					port:    80,
					tags: []string{
						"traefik.frontend.rule=Host:a.traefik.io",
						"traefik.enable=true",
					},
				},
				{
					name:    "foobar",
					address: "2.2.2.2",
					port:    80,
					tags: []string{
						"traefik.frontend.rule=Host:b.traefik.io",
						"traefik.enable=true",
					},
				},
				{
					name:    "foobar",
					address: "3.3.3.3",
					port:    80,
					tags: []string{
						"traefik.enable=true",
					},
				},
			},
			expected: &types.Configuration{
				Backends: map[string]*types.Backend{
					"backend-foobar-0": {
						Servers: map[string]types.Server{
							"foobar--1-1-1-1--80--0": {
								URL:    "http://1.1.1.1",
								Weight: 0,
							},
						},
					},
					"backend-foobar-1": {
						Servers: map[string]types.Server{
							"foobar--2-2-2-2--80--0": {
								URL:    "http://2.2.2.2",
								Weight: 0,
							},
						},
					},
					"backend-foobar-2": {
						Servers: map[string]types.Server{
							"foobar--3-3-3-3--80--0": {
								URL:    "http://3.3.3.3",
								Weight: 0,
							},
						},
					},
				},
				Frontends: map[string]*types.Frontend{
					"frontend-foobar-0": {
						EntryPoints: defaultEntrypoints,
						Backend:     "backend-foobar-0",
						Routes: map[string]types.Route{
							"route-foobar-0": {
								Rule: "Host:a.traefik.io",
							},
						},
						PassHostHeader: true,
						Priority:       0,
					},
					"frontend-foobar-1": {
						EntryPoints: defaultEntrypoints,
						Backend:     "backend-foobar-1",
						Routes: map[string]types.Route{
							"route-foobar-0": {
								Rule: "Host:b.traefik.io",
							},
						},
						PassHostHeader: true,
						Priority:       0,
					},
					"frontend-foobar-2": {
						EntryPoints: defaultEntrypoints,
						Backend:     "backend-foobar-0",
						Routes: map[string]types.Route{
							"route-foobar-0": {
								Rule: "Host:foobar-2.traefik",
							},
						},
						PassHostHeader: true,
						Priority:       0,
					},
				},
			},
		},
	}

	for _, ca := range cases {
		actualConfig := s.testE2eConfiguration(c, ca.nodes)
		if !reflect.DeepEqual(actualConfig.Backends, ca.expected.Backends) {
			c.Fatalf("expected %#v, got %#v", ca.expected.Backends, actualConfig.Backends)
		}
		if !reflect.DeepEqual(actualConfig.Frontends, ca.expected.Frontends) {
			c.Fatalf("expected %#v, got %#v", ca.expected.Frontends, actualConfig.Frontends)
		}
	}
}
