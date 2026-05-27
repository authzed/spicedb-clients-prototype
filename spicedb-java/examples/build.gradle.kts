plugins {
    java
}

dependencies {
    implementation(project(":lib"))

    testImplementation("org.junit.jupiter:junit-jupiter:6.1.0")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
    testImplementation("org.assertj:assertj-core:3.27.7")
}

tasks.test {
    useJUnitPlatform()
    // Run tests sequentially - they share a single SpiceDB instance
    maxParallelForks = 1
}
