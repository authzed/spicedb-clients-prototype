plugins {
    `java-library`
    `maven-publish`
}

// Coordinates shared with spicedb-java (com.authzed.spicedb:spicedb-java), which
// depends on this artifact at the same version. Change both together.
group = "com.authzed.spicedb"
version = "0.1.0"

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
    withSourcesJar()
}

repositories {
    mavenCentral()
}

// This module is the single JVM binding for authzed.api.v1 and
// authzed.api.materialize.v0 (root DESIGN.md, "Be the single copy of the
// protocol bindings in any process"). It compiles the buf output in gen/,
// which uses the upstream java_package (com.authzed.api.v1) -- the same fully
// qualified class names authzed-java ships -- so authzed-java can be bridged
// onto it by dropping its own copy (see DESIGN.md "Canonical binding" and the
// bridgeAuthzedJava task below). It must never depend on BSR generated SDKs
// (build.buf.gen:authzed_api_*), whose build.buf.gen.* classes are a second,
// independently generated copy of the same proto messages.
sourceSets {
    main {
        java {
            setSrcDirs(listOf("src/main/java", "gen"))
        }
    }
}

// Lowest versions that work (root DESIGN.md, "Document the path off the legacy
// libraries": library modules declare the lowest dependency versions they
// actually need). protobuf-java must be at least the gencode version stamped in
// gen/ ("Protobuf Java Version: 4.33.2"): the generated classes call
// RuntimeVersion.validateProtobufGencodeVersion and refuse an older runtime. The
// gencode version is pinned in buf.gen.yaml (protoc v33.2) at the floor
// proto-google-common-protos already needs and below the 4.34.2 Spring Boot 4.1
// manages. spicedb-java's ProtobufRuntime.FLOOR repeats this number.
val protobufFloor = "4.33.2"

dependencies {
    // Single source of truth for the gRPC stack -- see DESIGN.md "Invariants". Every
    // io.grpc:* coordinate below is deliberately versionless so a partial bump is
    // structurally impossible. It must be at least the grpc-java plugin version
    // that generated gen/'s *Grpc.java stubs.
    api(platform("io.grpc:grpc-bom:1.84.0"))

    // Types that appear in the generated public API (message/stub signatures).
    api("com.google.protobuf:protobuf-java:$protobufFloor")
    api("io.grpc:grpc-protobuf")
    api("io.grpc:grpc-stub")
    // com.google.rpc.Status appears in generated message signatures; com.google.api
    // annotations are referenced by descriptors. Same artifact authzed-java uses.
    api("com.google.api.grpc:proto-google-common-protos:2.65.0")
    // The default transport behind ManagedChannelBuilder.forTarget. No netty type appears in
    // this module's API, so consumers get it at run time without compiling against it.
    runtimeOnly("io.grpc:grpc-netty-shaded")

    // Descriptor-only imports of authzed/api's .proto files: generated classes
    // reference them only to build their file descriptors, never in signatures.
    // These are the canonical Maven artifacts for those protos -- the same ones
    // authzed-java depends on -- so no second copy of validate/buf.validate exists.
    implementation("build.buf.protoc-gen-validate:pgv-java-stub:1.3.0")
    implementation("build.buf:protovalidate:1.1.0") {
        // Only build.buf.validate.ValidateProto's descriptor is needed; the CEL
        // validation engine is the consumer's choice.
        isTransitive = false
    }

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

tasks.withType<JavaCompile>().configureEach {
    options.encoding = "UTF-8"
}

// `gradle publishToMavenLocal` installs com.authzed.spicedb:spicedb-java-proto into
// ~/.m2 for consumers building before the artifacts reach a public repository.
// versionMapping writes the resolved version of every dependency into the POM, so
// no <dependency> is versionless (the grpc-bom import stays as well).
publishing {
    publications {
        create<MavenPublication>("maven") {
            artifactId = "spicedb-java-proto"
            from(components["java"])
            versionMapping {
                usage("java-api") { fromResolutionOf("runtimeClasspath") }
                usage("java-runtime") { fromResolutionResult() }
            }
            pom {
                name.set("spicedb-java-proto")
                description.set("Generated gRPC and protobuf bindings for the SpiceDB API (authzed.api.v1)")
                url.set("https://github.com/authzed/spicedb-clients")
            }
        }
    }
}

// ---------------------------------------------------------------------------
// authzed-java bridge (root DESIGN.md: "Each proto tier ships the bridge
// generator for its legacy library and a test that loads both in one process").
//
// Java has no type aliases, so the bridge works at the artifact level: the
// published authzed-java jar is repackaged WITHOUT its generated
// com.authzed.api.** / grpc.gateway.** classes and bundled .proto files, keeping
// only its hand-written helpers (com.authzed.grpcutil.BearerToken), and its POM
// depends on this module instead. Because this module generates the identical
// fully qualified class names, code compiled against authzed-java links against
// this module's classes unchanged. See DESIGN.md "Canonical binding".
// ---------------------------------------------------------------------------
val legacyAuthzedJavaVersion = "1.6.0"

val legacyAuthzedJava: Configuration by configurations.creating {
    isTransitive = false
    isCanBeConsumed = false
}

dependencies {
    legacyAuthzedJava("com.authzed.api:authzed:$legacyAuthzedJavaVersion")
}

/** Paths in authzed-java's jar that duplicate this module and must be dropped. */
val bridgedAwayPaths =
    listOf(
        "com/authzed/api/**",
        "grpc/gateway/**",
        "authzed/**",
        "protoc-gen-openapiv2/**",
    )

val bridgeAuthzedJava by
    tasks.registering(Jar::class) {
        group = "bridge"
        description =
            "Repackages authzed-java $legacyAuthzedJavaVersion onto this module (drops its generated classes)."
        archiveBaseName.set("authzed")
        archiveVersion.set("$legacyAuthzedJavaVersion-spicedb-bridge")
        destinationDirectory.set(layout.buildDirectory.dir("bridge"))
        from({ legacyAuthzedJava.map { zipTree(it) } }) {
            exclude(bridgedAwayPaths)
            exclude("META-INF/MANIFEST.MF")
        }
        manifest {
            attributes(
                "Implementation-Title" to "authzed (bridged onto spicedb-java-proto)",
                "Implementation-Version" to "$legacyAuthzedJavaVersion-spicedb-bridge",
            )
        }
    }

/**
 * The POM the bridged artifact publishes with: authzed-java's own runtime needs
 * (grpc) come through spicedb-java-proto, which now provides com.authzed.api.v1.
 */
val bridgeAuthzedJavaPom by
    tasks.registering {
        group = "bridge"
        val out = layout.buildDirectory.file("bridge/authzed-$legacyAuthzedJavaVersion-spicedb-bridge.pom")
        outputs.file(out)
        val selfGroup = project.group.toString()
        val selfVersion = project.version.toString()
        doLast {
            out.get().asFile.writeText(
                """
                |<?xml version="1.0" encoding="UTF-8"?>
                |<project xmlns="http://maven.apache.org/POM/4.0.0">
                |  <modelVersion>4.0.0</modelVersion>
                |  <groupId>com.authzed.api</groupId>
                |  <artifactId>authzed</artifactId>
                |  <version>$legacyAuthzedJavaVersion-spicedb-bridge</version>
                |  <description>authzed-java with its generated classes replaced by spicedb-java-proto</description>
                |  <dependencies>
                |    <dependency>
                |      <groupId>$selfGroup</groupId>
                |      <artifactId>spicedb-java-proto</artifactId>
                |      <version>$selfVersion</version>
                |      <scope>compile</scope>
                |    </dependency>
                |  </dependencies>
                |</project>
                |""".trimMargin(),
            )
        }
    }

// The bridge test compiles a fixture against the ORIGINAL authzed-java jar (as a
// legacy consumer was compiled), then runs it with only this module plus the
// bridged jar on the class path.
val legacyFixture: SourceSet by sourceSets.creating {
    java.srcDir("src/legacyFixture/java")
    compileClasspath = legacyAuthzedJava + configurations["compileClasspath"]
}

tasks.named<JavaCompile>("compileLegacyFixtureJava") {
    // Compiled against authzed-java's own classes only -- never this module's.
    classpath = legacyAuthzedJava + configurations["compileClasspath"]
}

dependencies {
    testImplementation(files(legacyFixture.output))
    testRuntimeOnly(files(bridgeAuthzedJava))
}

tasks.test {
    useJUnitPlatform()
    dependsOn(bridgeAuthzedJava, bridgeAuthzedJavaPom)
    // Lets the test also prove the UNbridged pair would duplicate classes.
    systemProperty(
        "spicedb.legacyAuthzedJavaJar",
        legacyAuthzedJava.singleFile.absolutePath,
    )
    systemProperty(
        "spicedb.bridgedAuthzedJavaJar",
        bridgeAuthzedJava.get().archiveFile.get().asFile.absolutePath,
    )
}
