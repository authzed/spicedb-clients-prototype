plugins {
    java
    id("com.diffplug.spotless") version "8.10.0"
}

allprojects {
    group = "com.authzed"
    version = "0.1.0-SNAPSHOT"

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
}

subprojects {
    apply(plugin = "java")
    apply(plugin = "com.diffplug.spotless")

    java {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    tasks.withType<JavaCompile> {
        options.encoding = "UTF-8"
        options.compilerArgs.addAll(listOf("-Xlint:all", "-Xlint:-processing"))
    }

    configure<com.diffplug.gradle.spotless.SpotlessExtension> {
        java {
            googleJavaFormat("1.22.0")
            removeUnusedImports()
            trimTrailingWhitespace()
            endWithNewline()
        }
    }
}
