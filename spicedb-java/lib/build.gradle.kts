plugins {
    `java-library`
}

dependencies {
    api("com.authzed:spicedb-java-proto")

    api("io.grpc:grpc-api:1.79.0")
    implementation("io.grpc:grpc-stub:1.79.0")
    implementation("io.grpc:grpc-netty-shaded:1.79.0")
    implementation("io.grpc:grpc-protobuf:1.79.0")

    testImplementation("org.junit.jupiter:junit-jupiter:5.11.3")
    testImplementation("io.grpc:grpc-inprocess:1.79.0")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
}
