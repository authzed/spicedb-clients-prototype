plugins {
    `java-library`
}

group = "com.authzed.spicedb"
version = "0.1.0"

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

repositories {
    maven {
        name = "buf"
        url = uri("https://buf.build/gen/maven")
        content {
            includeGroup("build.buf.gen")
        }
    }
    mavenCentral()
}

sourceSets {
    main {
        java {
            srcDirs("src/main/java")
        }
    }
}

dependencies {
    // BSR Generated SDKs — pre-built proto stubs with all transitive deps resolved
    api("build.buf.gen:authzed_api_protocolbuffers_java:35.1.0.1.20260521180231.55aa23d533a3")
    api("build.buf.gen:authzed_api_grpc_java:1.83.1.1.20260521180231.55aa23d533a3")

    // Single source of truth for the gRPC stack -- see DESIGN.md "Invariants". Every
    // io.grpc:* coordinate below is deliberately versionless so a partial bump is
    // structurally impossible. This version must equal the gRPC version the BSR stubs
    // above are generated against (the leading component of their BSR version string).
    api(platform("io.grpc:grpc-bom:1.83.1"))

    api("io.grpc:grpc-netty-shaded")
    api("io.grpc:grpc-protobuf")
    api("io.grpc:grpc-stub")
    api("javax.annotation:javax.annotation-api:1.3.2")

    testImplementation(platform("org.junit:junit-bom:6.1.3"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")

    // In-process transport for insecure-host-guard tests: lets a test wire the
    // client to a real in-process server without opening a real socket, while
    // the "endpoint" string handed to the constructor -- what the guard
    // actually evaluates -- stays independent and can be a non-loopback
    // literal for the refusal/opt-in cases.
    testImplementation("io.grpc:grpc-inprocess")
    testImplementation("io.grpc:grpc-testing")
}

tasks.test {
    useJUnitPlatform()
}
