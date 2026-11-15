# syntax=docker/dockerfile:1
# Build konteksti — REPO ILDIZI (shared/ ham kerak):
#   docker build -f services/identity/Dockerfile .
FROM golang:1.26-alpine AS build
WORKDIR /src

# Avval faqat modul fayllari: bog'liqliklar keshi kod o'zgarganda buzilmasin
COPY shared/go.mod shared/go.mod
COPY services/identity/go.mod services/identity/go.sum services/identity/
WORKDIR /src/services/identity
RUN go mod download

WORKDIR /src
COPY shared/go.mod shared/
COPY shared/roles shared/roles
COPY shared/go shared/go
COPY services/identity services/identity
WORKDIR /src/services/identity
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/identity .

# distroless/static: shell yo'q, CA sertifikatlari bor, root bo'lmagan foydalanuvchi
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/identity /identity
ENV PORT=8010
EXPOSE 8010
USER nonroot:nonroot
ENTRYPOINT ["/identity"]
