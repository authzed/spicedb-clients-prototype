plugins {
    `java-library`
}

dependencies {
    api("com.authzed:spicedb-java-proto")

    // Single source of truth for the gRPC stack. Every io.grpc:* coordinate below is
    // deliberately versionless: grpc-java releases its artifacts in lockstep against
    // each other's internal SPIs, and the BOM makes a partial bump impossible rather
    // than merely discouraged. Declared `api` (not `implementation`) because the `api`
    // configuration does not extend `implementation`, so an implementation-scoped
    // platform would not govern the `api` coordinates below. Keep this version equal to
    // the one the BSR gRPC stubs are generated against -- see spicedb-java-proto's
    // DESIGN.md "Invariants".
    api(platform("io.grpc:grpc-bom:1.83.1"))

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
}
