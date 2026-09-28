FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/monerod-dashboard ./cmd/monerod-dashboard

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/monerod-dashboard /monerod-dashboard
# Listen on all interfaces inside the container; the default (127.0.0.1)
# would be unreachable through a published port.
ENV DASHBOARD_LISTEN=0.0.0.0:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/monerod-dashboard"]
