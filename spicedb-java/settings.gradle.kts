rootProject.name = "spicedb-java"

include("lib")
include("examples")

// Include the proto client as a composite build so it resolves as a project dependency.
includeBuild("../proto-clients/spicedb-java-proto") {
    dependencySubstitution {
        substitute(module("com.authzed:spicedb-java-proto")).using(project(":"))
    }
}
