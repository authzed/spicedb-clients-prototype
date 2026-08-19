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

    api("io.grpc:grpc-netty-shaded:1.83.1")
    api("io.grpc:grpc-protobuf:1.83.1")
    api("io.grpc:grpc-stub:1.83.1")
    api("javax.annotation:javax.annotation-api:1.3.2")

    testImplementation(platform("org.junit:junit-bom:6.1.3"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
}
