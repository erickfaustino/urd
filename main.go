package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/ericchiang/k8s"
	api "github.com/ericchiang/k8s/api/v1"
	"github.com/ghodss/yaml"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var cwClient *cloudwatch.CloudWatch
var httpRequestsTotal *prometheus.CounterVec
var backendConnectionsErrors *prometheus.CounterVec
var healthyHostCount *prometheus.GaugeVec
var elbLatency *prometheus.HistogramVec
var requestCount *prometheus.CounterVec
var spilloverCount *prometheus.CounterVec
var surgeQueueLength *prometheus.CounterVec
var unhealthyHostCount *prometheus.GaugeVec

type elbMetric struct {
	MetricName string
	Statistic  string
	Prometheus func(string, *string, *string, float64)
}

// metrics Struct maps all metrics available for AWS Classic ELBs and their respectives useful statistics.
var metrics = []elbMetric{
	{"HTTPCode_Backend_2XX", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		httpRequestsTotal.WithLabelValues("2XX", elbName, *svcName, *ns).Add(value)
	}},
	{"HTTPCode_Backend_3XX", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		httpRequestsTotal.WithLabelValues("3XX", elbName, *svcName, *ns).Add(value)
	}},
	{"HTTPCode_Backend_4XX", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		httpRequestsTotal.WithLabelValues("4XX", elbName, *svcName, *ns).Add(value)
	}},
	{"HTTPCode_Backend_5XX", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		httpRequestsTotal.WithLabelValues("5XX", elbName, *svcName, *ns).Add(value)
	}},
	{"HTTPCode_ELB_4XX", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		httpRequestsTotal.WithLabelValues("ELB_4XX", elbName, *svcName, *ns).Add(value)
	}},
	{"HTTPCode_ELB_5XX", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		httpRequestsTotal.WithLabelValues("ELB_5XX", elbName, *svcName, *ns).Add(value)
	}},
	{"BackendConnectionErrors", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		backendConnectionsErrors.WithLabelValues(elbName, *svcName, *ns).Add(value)
	}},
	{"HealthyHostCount", "Average", func(elbName string, svcName *string, ns *string, value float64) {
		healthyHostCount.WithLabelValues(elbName, *svcName, *ns).Set(value)
	}},
	{"Latency", "Average", func(elbName string, svcName *string, ns *string, value float64) {
		elbLatency.WithLabelValues(elbName, *svcName, *ns).Observe(value)
	}},
	{"RequestCount", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		requestCount.WithLabelValues(elbName, *svcName, *ns).Add(value)
	}},
	{"SpilloverCount", "Sum", func(elbName string, svcName *string, ns *string, value float64) {
		spilloverCount.WithLabelValues(elbName, *svcName, *ns).Add(value)
	}},
	{"SurgeQueueLength", "Maximum", func(elbName string, svcName *string, ns *string, value float64) {
		surgeQueueLength.WithLabelValues(elbName, *svcName, *ns).Add(value)
	}},
	{"UnHealthyHostCount", "Average", func(elbName string, svcName *string, ns *string, value float64) {
		unhealthyHostCount.WithLabelValues(elbName, *svcName, *ns).Set(value)
	}},
}

// Init function create the CloudWatch Client and initializes all Prometheus Counters, Gauge and Histogram to register metrics.
func init() {
	sess := session.Must(session.NewSession())
	cwClient = cloudwatch.New(sess)
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "urd_http_requests_total", Help: "Total of HTTP Requests"}, []string{"status", "elb_name", "svc_name", "namespace"})
	backendConnectionsErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "backend_connection_errors_total", Help: "Total of Backend connection errors"}, []string{"elb_name", "svc_name", "namespace"})
	healthyHostCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "urd_healthy_hosts_count", Help: "The number of healthy instances registered with load balance"}, []string{"elb_name", "svc_name", "namespace"})
	elbLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "urd_average_elb_latency", Help: "Average latency in seconds from ELB sent the request to a instance until instance starts to respond"}, []string{"elb_name", "svc_name", "namespace"})
	requestCount = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "urd_request_count", Help: "Total of requests in the last interval (60 seconds by default)"}, []string{"elb_name", "svc_name", "namespace"})
	spilloverCount = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "urd_spillovercount_total", Help: "The total number of requests that were rejected because the surge queue is full."}, []string{"elb_name", "svc_name", "namespace"})
	surgeQueueLength = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "urd_surge_queue_length", Help: "The total number of requests that are pending routing"}, []string{"elb_name", "svc_name", "namespace"})
	unhealthyHostCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "urd_unhealthy_hosts_count", Help: "The number of unhealthy instances registered with load balancer"}, []string{"elb_name", "svc_name", "namespace"})

	prometheus.MustRegister(httpRequestsTotal, backendConnectionsErrors, healthyHostCount, elbLatency, requestCount, spilloverCount, surgeQueueLength, unhealthyHostCount)
}

// If
func loadClient() (*k8s.Client, error) {
	kubeconfigPath := "/srv/kubernetes/kubeconfig"
	if kubeCfg := os.Getenv("URD_KUBECONFIG_PATH"); kubeCfg != "" {
		kubeconfigPath = kubeCfg
	}

	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal kubeconfig: %v", err)
	}
	return k8s.NewClient(&config)
}

func getAllServices() []api.Service {
	k8sClient, err := loadClient()
	if err != nil {
		log.Fatal(err)
	}

	namespaces, err := k8sClient.CoreV1().ListNamespaces(context.Background())
	services := make([]api.Service, 0)
	for _, namespace := range namespaces.Items {
		svc, err := k8sClient.CoreV1().ListServices(context.Background(), *namespace.Metadata.Name)
		if err != nil {
			log.Fatal(err)
		}

		for _, service := range svc.Items {
			if *service.Spec.Type == "LoadBalancer" {
				services = append(services, *service)
			}
		}
	}

	return services
}

func getElbMetric(elbName string, metricName string, statisticType string) *float64 {
	currentTime := time.Now()
	lastMinute := currentTime.Add(-1 * time.Minute)
	data, err := cwClient.GetMetricStatistics(&cloudwatch.GetMetricStatisticsInput{
		Dimensions: []*cloudwatch.Dimension{
			&cloudwatch.Dimension{
				Name:  aws.String("LoadBalancerName"),
				Value: aws.String(elbName),
			},
		},
		StartTime:  aws.Time(lastMinute),
		EndTime:    aws.Time(currentTime),
		MetricName: aws.String(metricName),
		Namespace:  aws.String("AWS/ELB"),
		Period:     aws.Int64(60),
		Statistics: []*string{aws.String(statisticType)},
	})
	if err != nil {
		log.Fatal(err)
	}
	if len(data.Datapoints) == 0 {
		r := float64(0)
		return &r
	}
	var value *float64
	dp := data.Datapoints[0]
	switch statisticType {
	case "Sum":
		value = dp.Sum
	case "Average":
		value = dp.Average
	case "Maximum":
		value = dp.Maximum
	case "Minimum":
		value = dp.Minimum
	}
	return value
}

// This function returns the real ELB name from ELB DNS.
// internal-a8280213c611d114o7340onc0d34252-152337689.us-east-1.elb.amazonaws.com -> a8280213c611d114o7340onc0d34252
func elbNameFromElbDNS(elbDNS string) string {
	re, err := regexp.Compile("(.*)(?:-[0-9]{6})")
	if err != nil {
		fmt.Println(err)
	}
	elbName := re.FindStringSubmatch(elbDNS)[1]
	return strings.TrimPrefix(elbName, "internal-")
}

func collectMetrics() {
	services := getAllServices()

	var wg sync.WaitGroup
	wg.Add(len(services) * len(metrics))

	getMetric := func(m elbMetric, s api.Service) {
		elbName := elbNameFromElbDNS(*s.Status.LoadBalancer.Ingress[0].Hostname)
		m.Prometheus(elbName, s.Metadata.Name, s.Metadata.Namespace, *getElbMetric(elbName, m.MetricName, m.Statistic))
		wg.Done()
	}

	for _, service := range services {
		for _, metric := range metrics {
			go getMetric(metric, service)
		}
	}

	wg.Wait()
}

func main() {
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":8080", nil)
	log.Println("Listening on :8080/metrics")

	for {
		log.Println("Begun to get CW data for all ELBs")
		begin := time.Now()
		collectMetrics()
		log.Println("All metrics collected.")
		timediff := time.Now().Sub(begin)
		log.Printf("Sleeping for %s", time.Minute-timediff)
		time.Sleep(time.Minute - timediff)
	}
}
