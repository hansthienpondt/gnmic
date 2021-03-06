`gnmic` supports exporting subscription updates to [influxDB](https://www.influxdata.com/products/influxdb-overview/) time series database

An influxdb output can be defined using the below format in `gnmic` config file under `outputs` section:

```yaml
outputs:
  output1:
    type: influxdb # required
    url: http://localhost:8086 # influxDB server address
    org: myOrg # empty if using influxdb1.8.x
    bucket: telemetry # string in the form database/retention-policy. Skip retention policy for the default on
    token: # influxdb 1.8.x use a string in the form: "username:password"
    batch-size: 1000 # number of points to buffer before writing to the server
    flush-timer: 10s # flush period after which the buffer is written to the server whether the batch_size is reached or not
    use-gzip: false
    enable-tls: false
    health-check-period: 30s # server health check period, used to recover from server connectivity failure
    debug: false # enable debug
    enable-metrics: false # NOT IMPLEMENTED boolean, enables the collection and export (via prometheus) of output specific metrics
    event-processors: # list of processors to apply on the message before writing
```

`gnmic` uses the [`event`](../output_intro#formats-examples) format to generate the measurements written to influxdb
