plugins {
    `java-library`
    `maven-publish`
}

// Published as com.authzed.spicedb:spicedb-java (the project directory is `lib`).
base {
    archivesName.set("spicedb-java")
}

java {
    withSourcesJar()
}

dependencies {
    // Versioned, so the published POM names the proto tier with its version (a versionless
    // coordinate makes Maven reject the POM and drop every transitive dependency). The composite
    // build in settings.gradle.kts substitutes the local proto project for it.
    api("com.authzed.spicedb:spicedb-java-proto:${project.version}")

    // Single source of truth for the gRPC stack. Every io.grpc:* coordinate below is
    // deliberately versionless: grpc-java releases its artifacts in lockstep against
    // each other's internal SPIs, and the BOM makes a partial bump impossible rather
    // than merely discouraged. Declared `api` (not `implementation`) because the `api`
    // configuration does not extend `implementation`, so an implementation-scoped
    // platform would not govern the `api` coordinates below. Keep this version equal to
    // the one the BSR gRPC stubs are generated against -- see spicedb-java-proto's
    // DESIGN.md "Invariants".
    api(platform("io.grpc:grpc-bom:1.84.0"))

    api("io.grpc:grpc-api")
    implementation("io.grpc:grpc-stub")
    implementation("io.grpc:grpc-netty-shaded")
    implementation("io.grpc:grpc-protobuf")

    testImplementation("org.junit.jupiter:junit-jupiter:6.1.3")
    testImplementation("io.grpc:grpc-inprocess")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
    // ProtobufRuntimeTest compares ProtobufRuntime.FLOOR with the gencode version stamped in the
    // proto tier's generated sources.
    systemProperty(
        "spicedb.protoGenDir",
        rootProject.projectDir.resolve("../proto-clients/spicedb-java-proto/gen").canonicalPath,
    )
}

// `gradle publishToMavenLocal` (after the same command in proto-clients/spicedb-java-proto)
// installs com.authzed.spicedb:spicedb-java into ~/.m2. versionMapping writes the resolved
// version of every dependency into the POM, so a Maven consumer needs only this one coordinate.
publishing {
    publications {
        create<MavenPublication>("maven") {
            artifactId = "spicedb-java"
            from(components["java"])
            versionMapping {
                usage("java-api") { fromResolutionOf("runtimeClasspath") }
                usage("java-runtime") { fromResolutionResult() }
            }
            pom {
                name.set("spicedb-java")
                description.set("Idiomatic Java client for SpiceDB")
                url.set("https://github.com/authzed/spicedb-clients")
            }
        }
    }
}
