# HexGate
HexGate is a lightweight, high-performance API Gateway built from scratch in Go.

![](img/gate.png)

## ✨Features
- **Dynamic Service Discovery**: Integrates directly with HashiCorp Consul. 
Backends are no longer static; `hexgate` automatically discovers, adds, and removes them in real-time as they register
or fail health checks.
- **Path-Based Routing**: Intelligently routes requests to different backend service pools based on the URL path
(e.g., /users/* -> user-service, /products/* -> product-service).
- **Zero-Downtime Hot Reloading**: Automatically detects changes to config.yaml (e.g., new services, auth keys)
- **JWT Authentication (RS256)**: Secures routes with a secure, asymmetric (RS256) JWT validation middleware.
- **Per-IP Rate Limiting**: Protects services with a per-service, per-IP token bucket rate limiter.
- **Built-in Observability**: Exposes a `/metrics` endpoint for Prometheus, tracking request rates, latencies, and response codes
## Design
![](img/architecture.png)
## 🚀 Getting Started
1. Prerequisites 
- Go 1.19 or later
- HashiCorp Consul (for service discovery)
- Docker & Docker Compose (for monitoring stack)

2. Run Consul
```bash
consul agent -dev
```
3. Run `hexgate`
```
go run .
```
4. Run the Test Backends
Open 3 new terminals and run the following commands, one in each:
```bash
# Terminal 3: A user service
go run test/backend.go -port 8081 -service "user-service"

# Terminal 4: Another user service
go run test/backend.go -port 8082 -service "user-service"

# Terminal 5: A product service
go run test/backend.go -port 8083 -service "product-service"
```
As you start each backend, you will see `hexgate`'s log update in real-time as it discovers them!

5. Run the Monitoring Stack (Optional)

This launches Prometheus and Grafana, pre-configured to scrape HexGate.

```bash
docker-compose up -d
```
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (login: admin / admin)

## 📊 Monitoring Dashboard
HexGate is pre-configured to work with the included Prometheus and Grafana stack.

### Grafana Setup
1. Navigate to http://localhost:3000 (login: admin / admin).
2. On the left menu, go to Connections (plug icon) -> Add new connection.
3. Select Prometheus.
4. For "Prometheus server URL", enter: http://prometheus:9090
5. Click "Save & Test". You should see a green success message.
6. On the left menu, click the Dashboard icon (four squares) -> New -> New Dashboard to start building.

![](img/grafana.png)

## 🧪 Testing the Gateway
### Test 1: Service Discovery & Routing
Now that your backends are running, send requests to the gateway:
```bash
# This will be routed to the "user-service" pool
curl http://localhost:8000/users/1
# Hello from user-service (Port 8081)

# Send again to see the load balancing
curl http://localhost:8000/users/2
# Hello from user-service (Port 8082)

# This will be routed to the "product-service" pool
curl http://localhost:8000/products/abc
# Hello from product-service (Port 8083)
```
Now, go to Terminal 3 (port 8081) and stop the server (Ctrl+C).
Wait a few seconds for Consul to detect the failure.

Now, all traffic for /users/ will go only to the remaining healthy backend:
```bash
curl http://localhost:8000/users/3
# Hello from user-service (Port 8082)
```

### Test 2: Authentication (JWT)
- Enable `authentication` in config/config.yaml file. All routes are now protected. A normal request will fail:
```bash
curl http://localhost:8000/users/1
# 401 Unauthorized: Missing Authorization header
```

Need to attach a JWT token to the header request
```
TOKEN=eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjE3NjE0NzY2NjksImlhdCI6MTc2MTM5MDI2OSwibmFtZSI6IlRlc3QgVXNlciIsInN1YiI6InVzZXItMTIzLWFiYyJ9.noYkvn-b005BoXJJEcm5YeDpkRBiNpkQ4MDN0c07nQwfhEtKb33ifToXz5LqsmcwHolnAmKX2mxfzfQm0v7BQki6fFex985WpvOGGobSxUAXMNEltT60Ees3TNDiViqTSw-q0T_CS6w5Rh5mrf53sExRlzkNLg30A0bNNuW_F91ICnxhi741U5B41JgBcAK3JVasJK6h-nTto_dWKftLNTR_sg9cSz7dDNlwoiB_8BpDP07l5L1Jf2Q5FrmFIBuQSELMz0ec2ON9s2YoL1L_vHS3_w5owSvVGQu1K1Yr3ZK6keOvdDbPY4yOQEOl0cTYZ9-HII_0-TbCBtXuZ8bEsw

curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/users/3
```

### Test 3: Rate Limiting
Our config is set to 1 request/sec with a burst of 3. Send 4 rapid requests:

```bash
# (Assumes you are sending the valid JWT token)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/users/1 # OK
curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/users/2 # OK
curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/users/3 # OK
curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/users/4
# 429 Too Many Requests
```
