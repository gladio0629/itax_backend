# syntax=docker/dockerfile:1
# Build konteksti — REPO ILDIZI (shared/ ham kerak):
#   docker build -f services/pricing/Dockerfile .
FROM golang:1.26-alpine AS build
WORKDIR /src

# Avval faqat modul fayllari: bog'liqliklar keshi kod o'zgarganda buzilmasin
COPY shared/go.mod shared/go.mod
COPY services/pricing/go.mod services/pricing/go.sum services/pricing/
WORKDIR /src/services/pricing
RUN go mod download

WORKDIR /src
COPY shared/go.mod shared/
COPY shared/geo shared/geo
COPY shared/go shared/go
COPY services/pricing services/pricing
WORKDIR /src/services/pricing
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pricing .

# distroless/static: shell yo'q, CA sertifikatlari bor, root bo'lmagan foydalanuvchi
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pricing /pricing
ENV PORT=8014
EXPOSE 8014
USER nonroot:nonroot
ENTRYPOINT ["/pricing"]
