# syntax=docker/dockerfile:1
# Build konteksti — REPO ILDIZI (shared/ ham kerak):
#   docker build -f services/trip-order/Dockerfile .
FROM golang:1.26-alpine AS build
WORKDIR /src

# Avval faqat modul fayllari: bog'liqliklar keshi kod o'zgarganda buzilmasin
COPY shared/go.mod shared/go.mod
COPY services/trip-order/go.mod services/trip-order/go.sum services/trip-order/
WORKDIR /src/services/trip-order
RUN go mod download

WORKDIR /src
COPY shared/go.mod shared/
COPY shared/go shared/go
COPY shared/geo shared/geo
COPY services/trip-order services/trip-order
WORKDIR /src/services/trip-order
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/trip-order .

# distroless/static: shell yo'q, CA sertifikatlari bor, root bo'lmagan foydalanuvchi
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/trip-order /trip-order
ENV PORT=8012
EXPOSE 8012
USER nonroot:nonroot
ENTRYPOINT ["/trip-order"]
