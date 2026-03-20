rootProject.name = "spicedb-gen-java-test"

// Include spicedb-java as a composite build for local project dependency
includeBuild("../../../spicedb-java") {
    dependencySubstitution {
        substitute(module("com.authzed:spicedb-java-lib")).using(project(":lib"))
    }
}
