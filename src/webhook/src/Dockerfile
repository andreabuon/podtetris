FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/podtetris-webhook

FROM scratch
COPY --from=build /out/podtetris-webhook /podtetris-webhook
ENTRYPOINT ["/podtetris-webhook"]
EXPOSE 8443