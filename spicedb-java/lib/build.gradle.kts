plugins {
    `java-library`
}

dependencies {
    api("com.authzed:spicedb-java-proto")

    api("io.grpc:grpc-api:1.83.1")
    implementation("io.grpc:grpc-stub:1.83.1")
    implementation("io.grpc:grpc-netty-shaded:1.83.1")
    implementation("io.grpc:grpc-protobuf:1.83.1")

    testImplementation("org.junit.jupiter:junit-jupiter:6.1.3")
    testImplementation("io.grpc:grpc-inprocess:1.83.1")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
}
