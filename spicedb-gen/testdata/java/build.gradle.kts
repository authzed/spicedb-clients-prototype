plugins {
    java
}

group = "com.authzed.spicedb.gen.test"
version = "0.0.1"

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

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

dependencies {
    implementation("com.authzed:spicedb-java-lib")
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

// Task to attempt compilation of type error files (expected to FAIL)
tasks.register<JavaCompile>("compileTypeErrors") {
    source = fileTree("type_errors")
    classpath = sourceSets["main"].compileClasspath + sourceSets["main"].output
    destinationDirectory.set(layout.buildDirectory.dir("type-errors"))
    options.compilerArgs.addAll(listOf("-Xlint:all", "-Xlint:-processing"))
}
