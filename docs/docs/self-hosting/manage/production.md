---
title: Production Setup
---

As soon as you successfully deployed ZITADEL as a proof of concept using one of our [deployment guides](/docs/self-hosting/deploy/overview),
you are ready to configure ZITADEL for production usage.


## TL;DR
We created a [Production Checklist](./productionchecklist.md) as a technical step by step guide.

## High Availability

We recommend running ZITADEL highly available using an orchestrator that schedules ZITADEL on multiple servers,
like [Kubernetes](/docs/self-hosting/deploy/kubernetes).
For keeping startup times fast when scaling ZITADEL,
you should also consider using separate jobs with `zitadel init` and `zitadel setup`,
so your workload containers just have to execute `zitadel start`.
Read more about separating the init and setup phases on the [Updating and Scaling page](/docs/self-hosting/manage/updating_scaling).

## Configuration

Read [on the configure page](/docs/self-hosting/manage/configure) about the available options you have to configure ZITADEL.

## Networking

- To make ZITADEL available at the domain of your choice, [you need to configure the ExternalDomain property](/docs/self-hosting/manage/custom-domain).
- To enable and restrict access to **HTTPS**, head over to [the description of your TLS options](/docs/self-hosting/manage/tls_modes).
- If you want to front ZITADEL with a reverse proxy, web application firewall or content delivery network, make sure to support **[HTTP/2](/docs/self-hosting/manage/http2)**.
- You can also refer to some **[example reverse proxy configurations](/docs/self-hosting/manage/reverseproxy/reverse_proxy)**.
- The ZITADEL Console web GUI uses many gRPC-Web stubs. This results in a fairly big JavaScript bundle. You might want to compress it using [Gzip](https://www.gnu.org/software/gzip/) or [Brotli](https://github.com/google/brotli).
- Serving and caching the assets using a content delivery network could improve network latencies and shield your ZITADEL runtime.

## Monitoring

By default, [**metrics**](/apis/observability/metrics) are exposed at /debug/metrics in OpenTelemetry (otel) format.

Also, you can enable **tracing** in the ZITADEL configuration.

```yaml
Tracing:
  # Choose one in "otel", "google", "log" and "none"
  Type: google
  Fraction: 1
  MetricPrefix: zitadel
```

## Logging

ZITADEL follows the principles that guide cloud-native and twelve factor applications.
Logs are a stream of time-ordered events collected from all running processes.

ZITADEL processes write the following events to the standard output:

- Runtime Logs: Define the log level and record format [in the Log configuration section](https://github.com/zitadel/zitadel/blob/main/cmd/defaults.yaml#L1-L4)
- Access Logs: Enable logging all HTTP and gRPC responses from the ZITADEL binary [in the LogStore section](https://github.com/zitadel/zitadel/blob/main/cmd/defaults.yaml#L366) 
- Actions Exectution Logs: Actions can emit custom logs at different levels. For example, a log record can be emitted each time a user is created or authenticated. If you don't want to have these logs in STDOUT, you can disable this [in the LogStore section](https://github.com/zitadel/zitadel/blob/main/cmd/defaults.yaml#L387) .

Log file management should not be in each business apps responsibility.
Instead, your execution environment should provide tooling for managing logs in a generic way.
This includes tasks like rotating files, routing, collecting, archiving and cleaning-up.
For example, systemd has journald and kubernetes has fluentd and fluentbit.

## Database

### Prefer CockroachDB

ZITADEL supports [CockroachDB](https://www.cockroachlabs.com/) and [PostgreSQL](https://www.postgresql.org/).
We highly recommend using CockroachDB,
as horizontal scaling is much easier than with PostgreSQL.
Also, if you are concerned about multi-regional data locality,
[the way to go is with CockroachDB](https://www.cockroachlabs.com/docs/stable/multiregion-overview.html).

The indexes for the database are optimized using load tests from [ZITADEL Cloud](https://zitadel.com), 
which runs with CockroachDB.
If you identify problems with your Postgresql during load tests that indicate that the indexes are not optimized,
please create an issue in our [github repository](https://github.com/zitadel/zitadel).

### Configure ZITADEL

Depending on your environment, you maybe would want to tweak some settings about how ZITADEL interacts with the database in the database section of your ZITADEL configuration. Read more about your [database configuration options](/docs/self-hosting/manage/database).

```yaml
Database:
  cockroach:
    Host: localhost
    Port: 26257
    Database: zitadel
    //highlight-start
    MaxOpenConns: 20
    MaxConnLifetime: 30m
    MaxConnIdleTime: 30m
    //highlight-end
    Options: ""
```

You also might want to configure how [projections](/concepts/eventstore/implementation#projections) are computed. These are the default values:

```yaml
# The Projections section defines the behaviour for the scheduled and synchronous events projections.
Projections:
  # Time interval between scheduled projections
  RequeueEvery: 60s
  # Time between retried database statements resulting from projected events
  RetryFailedAfter: 1s
  # Retried execution number of database statements resulting from projected events
  MaxFailureCount: 5
  # Number of concurrent projection routines. Values of 0 and below are overwritten to 1
  ConcurrentInstances: 1
  # Limit of returned events per query
  BulkLimit: 200
  # Only instance are projected, for which at least a projection relevant event exists withing the timeframe
  # from HandleActiveInstances duration in the past until the projections current time
  # Defaults to twice the RequeueEvery duration
  HandleActiveInstances: 120s
  # In the Customizations section, all settings from above can be overwritten for each specific projection
  Customizations:
    Projects:
      BulkLimit: 2000
    # The Notifications projection is used for sending emails and SMS to users
    Notifications:
      # As notification projections don't result in database statements, retries don't have an effect
      MaxFailureCount: 0
    # The NotificationsQuotas projection is used for calling quota webhooks
    NotificationsQuotas:
      # Delivery guarantee requirements are probably higher for quota webhooks
      # Defaults to 45 days
      HandleActiveInstances: 1080h
      # As quota notification projections don't result in database statements, retries don't have an effect
      MaxFailureCount: 0
      # Quota notifications are not so time critical. Setting RequeueEvery every five minutes doesn't annoy the db too much.
      RequeueEvery: 300s
```

### Manage your data

When designing your backup strategy,
it is worth knowing that
[ZITADEL is event sourced](/docs/concepts/eventstore/overview).
That means, ZITADEL itself is able to recompute its
whole state from the records in the table eventstore.events.
The timestamp of your last record in the events table
defines up to which point in time ZITADEL can restore its state.

The ZITADEL binary itself is stateless,
so there is no need for a special backup job.

Generally, for maintaining your database management system in production,
please refer to the corresponding docs
[for CockroachDB](https://www.cockroachlabs.com/docs/stable/recommended-production-settings.html)
or [for PostgreSQL](https://www.postgresql.org/docs/current/admin.html).


## Data initialization

- You can configure instance defaults in the DefaultInstance section.
  If you plan to eventually create [multiple virtual instances](/concepts/structure/instance#multiple-virtual-instances), these defaults take effect.
  Also, these configurations apply to the first instance, that ZITADEL automatically creates for you.
  Especially the following properties are of special interest for your production setup.

```yaml
DefaultInstance:
  OIDCSettings:
    AccessTokenLifetime: 12h
    IdTokenLifetime: 12h
    RefreshTokenIdleExpiration: 720h #30d
    RefreshTokenExpiration: 2160h #90d
  # this configuration sets the default email configuration
  SMTPConfiguration:
    # configuration of the host
    SMTP:
      #for example smtp.mailtrap.io:2525
      Host:
      User:
      Password:
    TLS:
    # if the host of the sender is different from ExternalDomain set DefaultInstance.DomainPolicy.SMTPSenderAddressMatchesInstanceDomain to false
    From:
    FromName:
```

- If you don't want to use the DefaultInstance configuration for the first instance that ZITADEL automatically creates for you during the [setup phase](/self-hosting/manage/configure#database-initialization), you can provide a FirstInstance YAML section using the --steps argument.
- Learn how to configure ZITADEL via the [Console user interface](/guides/manage/console/overview).
- Probably, you also want to [apply your custom branding](/guides/manage/customize/branding), [hook into certain events](/guides/manage/customize/behavior), [customize texts](/guides/manage/customize/texts) or [add metadata to your users](/guides/manage/customize/user-metadata).
- If you want to automatically create ZITADEL resources, you can use the [ZITADEL Terraform Provider](/guides/manage/terraform/basics).

## Quotas

If you host ZITADEL as a service,
you might want to [limit usage and/or execute tasks on certain usage units and levels](/self-hosting/manage/quotas).

## Minimum system requirements

### General resource usage

ZITADEL consumes around 512MB RAM and can run with less than 1 CPU core.
The database consumes around 2 CPU under normal conditions and 6GB RAM with some caching to it.

:::info Password hashing
Be aware of CPU spikes when hashing passwords. We recommend to have 4 CPU cores available for this purpose.
:::

### Production HA cluster

It is recommended to build a minimal high-availability with 3 Nodes with 4 CPU and 16GB memory each.
Excluding non-essential services, such as log collection, metrics etc, the resources could be reduced to around 4 CPU and  8GB memory each.
