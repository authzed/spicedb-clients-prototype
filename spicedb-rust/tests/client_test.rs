use spicedb::client::SpiceDBClient;

#[tokio::test]
async fn test_new_plaintext_creates_client() {
    let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken").await;
    assert!(client.is_ok());
}

#[tokio::test]
async fn test_new_system_tls_creates_client() {
    let client = SpiceDBClient::new_system_tls("grpc.example.com:443", "my-token").await;
    assert!(client.is_ok());
}

#[tokio::test]
async fn test_builder_plaintext() {
    let client = SpiceDBClient::builder("localhost:50051", "token")
        .plaintext()
        .build()
        .await;
    assert!(client.is_ok());
}

#[tokio::test]
async fn test_builder_default() {
    let client = SpiceDBClient::builder("grpc.example.com:443", "token")
        .build()
        .await;
    assert!(client.is_ok());
}

#[tokio::test]
async fn test_builder_accepts_owned_strings() {
    let endpoint = String::from("localhost:50051");
    let token = String::from("my-token");
    let client = SpiceDBClient::builder(endpoint, token)
        .plaintext()
        .build()
        .await;
    assert!(client.is_ok());
}
