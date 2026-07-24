package mqhttp

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ajitg/vantage/internal/server"
)

// MetricsHandler exposes the broker's state as Prometheus metrics on a
// dedicated registry. All series derive from MQServer.Stats() at scrape time —
// a custom Collector, so the broker hot paths carry zero instrumentation and
// the numbers always agree with /api/v1/queue/inspect.
//
// Route: GET /metrics
func MetricsHandler(srv *server.MQServer) http.HandlerFunc {
	reg := prometheus.NewRegistry()
	reg.MustRegister(&mqCollector{srv: srv})
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP
}

// mqCollector adapts ServerStats to the Prometheus exposition format.
type mqCollector struct {
	srv *server.MQServer
}

var (
	descDepth        = prometheus.NewDesc("mq_queue_depth", "Messages queued for first delivery (main lane).", nil, nil)
	descRetryDepth   = prometheus.NewDesc("mq_retry_depth", "Messages awaiting redelivery in the retry lane.", nil, nil)
	descDLQDepth     = prometheus.NewDesc("mq_dlq_depth", "Messages currently parked in the dead-letter lane.", nil, nil)
	descCapacity     = prometheus.NewDesc("mq_capacity", "Total capacity budget (queued + in-flight).", nil, nil)
	descInFlight     = prometheus.NewDesc("mq_in_flight", "Messages delivered but not yet acked.", nil, nil)
	descConsumers    = prometheus.NewDesc("mq_active_consumers", "Currently connected Consume streams.", nil, nil)
	descProduced     = prometheus.NewDesc("mq_produced_total", "Messages accepted by Produce.", nil, nil)
	descRejected     = prometheus.NewDesc("mq_rejected_total", "Produce refusals under backpressure (not loss).", nil, nil)
	descDelivered    = prometheus.NewDesc("mq_delivered_total", "Messages sent to consumers.", nil, nil)
	descConsumed     = prometheus.NewDesc("mq_consumed_total", "Acks received (confirmed deliveries).", nil, nil)
	descRedelivered  = prometheus.NewDesc("mq_redelivered_total", "Messages requeued for redelivery.", nil, nil)
	descDropped      = prometheus.NewDesc("mq_dropped_total", "Messages dropped, by cause.", []string{"cause"}, nil)
	descDeadLettered = prometheus.NewDesc("mq_dead_lettered_total", "Messages routed to the dead-letter lane.", nil, nil)
	descLeaseExpired = prometheus.NewDesc("mq_lease_expired_total", "Leases reclaimed by the TTL sweeper.", nil, nil)
)

func (c *mqCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

func (c *mqCollector) Collect(ch chan<- prometheus.Metric) {
	st := c.srv.Stats()
	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
	}
	gauge(descDepth, float64(st.Depth))
	gauge(descRetryDepth, float64(st.RetryDepth))
	gauge(descDLQDepth, float64(st.DLQDepth))
	gauge(descCapacity, float64(st.Capacity))
	gauge(descInFlight, float64(st.InFlight))
	gauge(descConsumers, float64(st.ActiveConsumers))
	counter(descProduced, float64(st.Produced))
	counter(descRejected, float64(st.Rejected))
	counter(descDelivered, float64(st.Delivered))
	counter(descConsumed, float64(st.Consumed))
	counter(descRedelivered, float64(st.Redelivered))
	counter(descDropped, float64(st.DroppedOverflow), "overflow")
	counter(descDropped, float64(st.DroppedRequeue), "requeue")
	counter(descDeadLettered, float64(st.DeadLettered))
	counter(descLeaseExpired, float64(st.LeaseExpired))
}
