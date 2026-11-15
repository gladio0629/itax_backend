plugins {
    kotlin("jvm") version "2.0.21"
    kotlin("plugin.spring") version "2.0.21"
    id("org.springframework.boot") version "3.3.5"
    id("io.spring.dependency-management") version "1.1.6"
}

group = "com.izgo"
version = "0.1.0"

repositories {
    mavenCentral()
}

kotlin {
    jvmToolchain(21)
    compilerOptions {
        freeCompilerArgs.add("-Xjsr305=strict")
    }
}

dependencies {
    implementation("org.springframework.boot:spring-boot-starter-webflux")
    implementation("org.springframework.boot:spring-boot-starter-validation")
    implementation("org.springframework.boot:spring-boot-starter-data-r2dbc")
    implementation("org.springframework.boot:spring-boot-starter-data-redis-reactive")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin")
    implementation("io.projectreactor.kotlin:reactor-kotlin-extensions")
    implementation("org.jetbrains.kotlin:kotlin-reflect")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-reactor")

    // Reaktiv drayver (ish vaqtida)
    runtimeOnly("org.postgresql:r2dbc-postgresql")

    // Flyway migratsiyalari JDBC orqali ishlaydi (R2DBC bilan Flyway ishlamaydi).
    // Flyway 10+ da PostgreSQL qo'llab-quvvatlash alohida modulda.
    implementation("org.springframework:spring-jdbc") // Flyway o'z DataSource'ini (spring.flyway.url) shu orqali yaratadi
    implementation("org.flywaydb:flyway-core")
    implementation("org.flywaydb:flyway-database-postgresql")
    runtimeOnly("org.postgresql:postgresql")

    // NATS JetStream
    implementation("io.nats:jnats:2.20.5")

    testImplementation("org.springframework.boot:spring-boot-starter-test")
    testImplementation("io.mockk:mockk:1.13.12")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
}

// Dockerfile uchun barqaror nom: build/libs/app.jar
tasks.bootJar {
    archiveFileName.set("app.jar")
}
tasks.jar {
    enabled = false
}
