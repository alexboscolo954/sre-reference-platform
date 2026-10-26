# docker buildx build --build-arg SERVICE=storefront|checkout -f apps/Dockerfile apps
FROM --platform=$BUILDPLATFORM golang:1.27.3 AS build
ARG SERVICE
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/app ./cmd/$SERVICE

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
