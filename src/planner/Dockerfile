FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
COPY podtetris-scheduler-config.yaml ./
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build -o /out/podtetris-planner

FROM scratch
WORKDIR /
COPY --from=build /out/podtetris-planner /podtetris-planner
COPY --from=build /src/podtetris-scheduler-config.yaml /podtetris-scheduler-config.yaml
ENTRYPOINT ["/podtetris-planner"]
