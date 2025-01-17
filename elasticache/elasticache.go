package elasticache

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/integralist/go-findroot/find"
	"github.com/thompsonlabs/go-elasticache/lister"
)

// Node is a single ElastiCache node
type Node struct {
	URL  string
	Host string
	IP   string
	Port int
}

// Item embeds the memcache client's type of the same name
type Item memcache.Item

// Client embeds the memcache client so we can hide those details away
type Client struct {
	*memcache.Client
	clusterNodeLister *lister.ClusterNodesKeyLister
}

// Set abstracts the memcache client details away,
// by copying over the values provided by the user into the Set method,
// as coercing the custom Item type to the required memcache.Item type isn't possible.
// Downside is if memcache client fields ever change, it'll introduce a break
func (c *Client) Set(item *Item) error {
	return c.Client.Set(&memcache.Item{
		Key:        item.Key,
		Value:      item.Value,
		Expiration: item.Expiration,
	})
}

var logger *log.Logger

//new var to hold endpoint environment variable name this instance should use.
//this allows each instance of this class to be associated with their own endpoint.
var endpointEnvironmentVarName string

func init() {
	logger = log.New(os.Stdout, "go-elasticache: ", log.Ldate|log.Ltime|log.Lshortfile)

	if env := os.Getenv("APP_ENV"); env == "test" {
		root, err := find.Repo()
		if err != nil {
			log.Printf("Repo Error: %s", err.Error())
		}

		path := fmt.Sprintf("%s/go-elasticache.log", root.Path)

		file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
		if err != nil {
			log.Printf("Open File Error: %s", err.Error())
		}

		logger = log.New(file, "go-elasticache: ", log.Ldate|log.Ltime|log.Lshortfile)
	}
}

// New returns an instance of the memcache client
func New() (*Client, error) {
	endpointEnvironmentVarName = "ELASTICACHE_ENDPOINT"
	urls, err := clusterNodes()
	if err != nil {
		return &Client{Client: memcache.New()}, err
	}

	return &Client{Client: memcache.New(urls...),
		clusterNodeLister: lister.NewClusterNodeKeyLister(urls)}, nil
}

// NewInstance - returns an instance of the memcache client, this alternative constructor
//               allows an endpoint environment variable to be specified specific to this
//               instance. Where a value is not provided the default value: ELASTICACHE_ENDPOINT
//               will be used.
func NewInstance(endpointEnvVarName string) (*Client, error) {
	if len(endpointEnvVarName) < 1 {
		endpointEnvironmentVarName = "ELASTICACHE_ENDPOINT"
	} else {
		endpointEnvironmentVarName = endpointEnvVarName
	}

	urls, err := clusterNodes()
	if err != nil {
		return &Client{Client: memcache.New()}, err
	}

	return &Client{Client: memcache.New(urls...),
		clusterNodeLister: lister.NewClusterNodeKeyLister(urls)}, nil
}

//Lists all keys stored accross all nodes in the Cluster.
func (c *Client) ListAllKeys() (*[]string, error) {

	return c.clusterNodeLister.ListAllHostKeys()
}

func clusterNodes() ([]string, error) {
	endpoint, err := elasticache()
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("tcp", endpoint)
	if err != nil {
		logger.Printf("Socket Dial (%s): %s", endpoint, err.Error())
		return nil, err
	}
	defer conn.Close()

	command := "config get cluster\r\n"
	fmt.Fprintf(conn, command)

	response, err := parseNodes(conn)
	if err != nil {
		return nil, err
	}

	urls, err := parseURLs(response)
	if err != nil {
		return nil, err
	}

	return urls, nil
}

func elasticache() (string, error) {
	var endpoint string

	endpoint = os.Getenv(endpointEnvironmentVarName)
	if len(endpoint) == 0 {
		logger.Println("ElastiCache endpoint not set")
		return "", errors.New("ElastiCache endpoint not set")
	}

	return endpoint, nil
}

func parseNodes(conn io.Reader) (string, error) {
	var response string

	count := 0
	location := 3 // AWS docs suggest that nodes will always be listed on line 3

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		count++
		if count == location {
			response = scanner.Text()
		}
		if scanner.Text() == "END" {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Println("Scanner: ", err.Error())
		return "", err
	}

	logger.Println("ElastiCache nodes found: ", response)
	return response, nil
}

func parseURLs(response string) ([]string, error) {
	var urls []string
	var nodes []Node

	items := strings.Split(response, " ")

	for _, v := range items {
		fields := strings.Split(v, "|") // ["host", "ip", "port"]

		port, err := strconv.Atoi(fields[2])
		if err != nil {
			logger.Println("Integer conversion: ", err.Error())
			return nil, err
		}

		node := Node{fmt.Sprintf("%s:%d", fields[1], port), fields[0], fields[1], port}
		nodes = append(nodes, node)
		urls = append(urls, node.URL)

		logger.Printf("Host: %s, IP: %s, Port: %d, URL: %s", node.Host, node.IP, node.Port, node.URL)
	}

	return urls, nil
}
