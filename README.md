# urd

## Who is Urd?

![urd](https://upload.wikimedia.org/wikipedia/commons/thumb/8/85/Urd_magazine.jpg/400px-Urd_magazine.jpg)

Urðr in the norse mithology was one of the Norns, along with Verðandi and Skuld.

She is the guardian of the past and is always looking backwards.  

## Why and What is this?

Inside Kubernetes, sometimes you have Service objects of type "LoadBalancer". The cluster manages it automatically, a random name is generated and if you want to get the AWS ELB metrics, you need to dig into LoadBalancers and then get the metrics from CloudWatch. If the Service is deleted and recreated, you will lose your previous metrics.
Urd discovers all the LoadBalancers in your cluster, and then export the ELB metrics, so you can use the prometheus to scrape it and get this metrics with Grafana.

## Run

```
docker run -p 8080:8080  -v /tmp/kubeconfig:/tmp/kubeconfig -e AWS_ACCESS_KEY_ID=AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY=AWS_SECRET_ACCESS_KEY -e URD_KUBECONFIG_PATH=/tmp/kubeconfig erickfaustino/urd:v0.0.1
```

The metrics will be exposed on :8080/metrics

## Environment Variables

You only need to set 3 environment variables:

**AWS_ACCESS_KEY_ID**

The AWS Access Key

**AWS_SECRET_ACCESS_KEY**

AWS Secret Access Key

This Keys needs permissions on EC2 and CloudWatch


**KUBECONFIG**

The path for a valid kubeconfig file with permission to describe all namespaces and get all services.

## TODO

- Improve documentation.
- Use the official kubernetes client for golang.
- Support configuration for each service via annotations, like time range of the metrics and if the service will be scraped or not.
- Add more metrics

## Know issues

When you LoadBalancer listeners are TCP, HTTP metrics are not available, such HTTP requests by statuses, because CloudWatch does not report it.
