# Builds the example app. Apps copy this file and change the package path.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app ./examples/notes

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /app /app
USER nonroot:nonroot
EXPOSE 8080
ENV APP_HTTP_ADDR=0.0.0.0:8080
ENTRYPOINT ["/app"]
CMD ["serve"]
