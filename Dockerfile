FROM golang:1.26.6-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rc ./cmd/rc \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockreceiver ./cmd/mockreceiver

FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/rc /app/rc
COPY --from=build /out/mockreceiver /app/mockreceiver
USER app
EXPOSE 8080 8081
ENTRYPOINT ["/app/rc"]
CMD ["serve"]
