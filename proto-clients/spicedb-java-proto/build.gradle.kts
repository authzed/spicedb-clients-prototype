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
    api("build.buf.gen:authzed_api_protocolbuffers_java:34.0.0.1.20260217075218.5b2fc906e1a2")
    api("build.buf.gen:authzed_api_grpc_java:1.79.0.2.20260217075218.5b2fc906e1a2")

    api("io.grpc:grpc-netty-shaded:1.72.0")
    api("io.grpc:grpc-protobuf:1.72.0")
    api("io.grpc:grpc-stub:1.72.0")
    api("javax.annotation:javax.annotation-api:1.3.2")

    testImplementation(platform("org.junit:junit-bom:5.11.4"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")

    // In-process transport for insecure-host-guard tests: lets a test wire the
    // client to a real in-process server without opening a real socket, while
    // the "endpoint" string handed to the constructor -- what the guard
    // actually evaluates -- stays independent and can be a non-loopback
    // literal for the refusal/opt-in cases.
    testImplementation("io.grpc:grpc-inprocess:1.72.0")
    testImplementation("io.grpc:grpc-testing:1.72.0")
}

tasks.test {
    useJUnitPlatform()
}
