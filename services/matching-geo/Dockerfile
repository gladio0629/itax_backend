# syntax=docker/dockerfile:1
# Build konteksti — REPO ILDIZI (shared/ ham kerak):
#   docker build -f services/matching-geo/Dockerfile .
FROM golang:1.26-alpine AS build
WORKDIR /src

# Avval faqat modul fayllari: bog'liqliklar keshi kod o'zgarganda buzilmasin
COPY shared/go.mod shared/go.mod
COPY services/matching-geo/go.mod services/matching-geo/go.sum services/matching-geo/
WORKDIR /src/services/matching-geo
RUN go mod download

WORKDIR /src
COPY shared/go.mod shared/
COPY shared/geo shared/geo
COPY shared/go shared/go
COPY services/matching-geo services/matching-geo
WORKDIR /src/services/matching-geo
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/matching-geo .

# distroless/static: shell yo'q, CA sertifikatlari bor, root bo'lmagan foydalanuvchi
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/matching-geo /matching-geo
ENV PORT=8013
EXPOSE 8013
USER nonroot:nonroot
ENTRYPOINT ["/matching-geo"]
