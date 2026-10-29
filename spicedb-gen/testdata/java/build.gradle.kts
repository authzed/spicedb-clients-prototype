plugins {
    java
    // Same plugin and google-java-format version as spicedb-java (root DESIGN.md: generated output
    // is formatted with the language's canonical formatter). spotlessCheck verifies the generated
    // Permissions.java is already google-java-format clean; nothing here rewrites it.
    id("com.diffplug.spotless") version "8.10.3"
}

group = "com.authzed.spicedb.gen.test"
version = "0.0.1"

repositories {
    mavenCentral()
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

dependencies {
    implementation("com.authzed.spicedb:spicedb-java:0.1.0")
    testImplementation("org.junit.jupiter:junit-jupiter:5.11.3")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
    testImplementation("org.assertj:assertj-core:3.26.3")
}

tasks.test {
    useJUnitPlatform()
    maxParallelForks = 1
}

tasks.withType<JavaCompile> {
    options.encoding = "UTF-8"
    options.compilerArgs.addAll(listOf("-Xlint:all", "-Xlint:-processing"))
}

spotless {
    java {
        target("src/main/java/**/Permissions.java")
        googleJavaFormat("1.22.0")
    }
}

// Compiles the negative type fixtures (expected to FAIL). -Xmaxerrs makes javac report every
// error, so the Magefile can require an error on each line marked "should not compile".
tasks.register<JavaCompile>("compileTypeErrors") {
    source = fileTree("type_errors")
    classpath = sourceSets["main"].compileClasspath + sourceSets["main"].output
    destinationDirectory.set(layout.buildDirectory.dir("type-errors"))
    options.compilerArgs.addAll(listOf("-Xlint:all", "-Xlint:-processing", "-Xmaxerrs", "100000"))
}
