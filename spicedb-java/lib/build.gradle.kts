plugins {
    `java-library`
}

dependencies {
    api("com.authzed:spicedb-java-proto")

    api("io.grpc:grpc-api:1.81.0")
    implementation("io.grpc:grpc-stub:1.81.0")
    implementation("io.grpc:grpc-netty-shaded:1.81.0")
    implementation("io.grpc:grpc-protobuf:1.81.0")

    testImplementation("org.junit.jupiter:junit-jupiter:6.1.0")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
}
