#endpoints
POST /device/[device id]/info
POST /device/[device id]/telemetry
GET /device/[device id]

# configuration
over toml file with text file to list registered devices  

# dependencies to install and run
keydb
postgres

# start server
./gosrv conf.toml

# test messages
curl http://localhost:8080/device/01/info  -H "Content-Type:application/json;charset=utf-8" -d '{"info":"ok"}'  --request POST
curl http://localhost:8080/device/01/telemetry  -H "Content-Type:application/json;charset=utf-8" -d '{"telemetry":"done"}'  --request POST
curl http://localhost:8080/device/01  -H "Content-Type:application/json;charset=utf-8" -d '{}'  --request GET
