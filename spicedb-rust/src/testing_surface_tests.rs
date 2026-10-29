//! Keeps the double's surface equal to the client's. Every client method is
//! named by one [`Operation`]; the exhaustive match in `exercise` fails to
//! compile when an operation is added without being exercised here, and the
//! tests fail when a client method does not reach the double, does not consult
//! the fault hook, or (when the double does not model it) does not fail as
//! `UNIMPLEMENTED` through the real client.

use std::num::NonZeroU32;

use futures::StreamExt;

use super::*;
use crate::consistency;
use crate::*;

const ALL: [Operation; 26] = [
    Operation::Check,
    Operation::CheckBulk,
    Operation::Write,
    Operation::ReadRelationships,
    Operation::DeleteRelationships,
    Operation::DeleteAllRelationshipsNonAtomic,
    Operation::LookupResources,
    Operation::LookupSubjects,
    Operation::ExpandPermissionTree,
    Operation::ImportRelationships,
    Operation::ExportRelationships,
    Operation::Updates,
    Operation::ReadSchema,
    Operation::WriteSchema,
    Operation::ReflectSchema,
    Operation::ComputablePermissions,
    Operation::DependentRelations,
    Operation::DiffSchema,
    Operation::ExperimentalRegisterRelationshipCounter,
    Operation::ExperimentalCountRelationships,
    Operation::ExperimentalUnregisterRelationshipCounter,
    Operation::ExperimentalCountRelationshipsByFilter,
    Operation::ExperimentalRoaringLookupResources,
    Operation::WatchPermissionSets,
    Operation::LookupPermissionSets,
    Operation::DownloadPermissionSets,
];

/// Position in [`ALL`]. `exercise` matches exhaustively, so a new operation
/// must be handled there; this catches one handled but not listed.
fn ordinal(operation: Operation) -> usize {
    ALL.iter()
        .position(|o| *o == operation)
        .unwrap_or_else(|| panic!("{operation} is missing from ALL"))
}

/// The first error a stream yields, if any.
async fn first<T>(
    stream: impl futures::Stream<Item = Result<T, SpiceDBError>>,
) -> Result<(), SpiceDBError> {
    let mut stream = std::pin::pin!(stream);
    match stream.next().await {
        Some(Err(e)) => Err(e),
        _ => Ok(()),
    }
}

/// What calling one client method against the double did.
struct Outcome {
    /// The operation the double records the RPC under.
    recorded: Operation,
    /// Whether the double gives the operation in-memory semantics.
    modeled: bool,
    result: Result<(), SpiceDBError>,
}

fn outcome<T>(recorded: Operation, modeled: bool, result: Result<T, SpiceDBError>) -> Outcome {
    Outcome {
        recorded,
        modeled,
        result: result.map(drop),
    }
}

/// Calls the client method named by `operation`.
async fn exercise(client: &SpiceDBClient, operation: Operation) -> Outcome {
    let rel: Relationship = "document:a#viewer@user:x".parse().unwrap();
    let resource = rel.resource().clone();
    let subject = rel.subject().clone();
    let full = consistency::full;
    let one = NonZeroU32::new(1).unwrap();
    let op = operation;
    match operation {
        Operation::Check => {
            let check = PermissionCheck::new(resource, "view", subject, full());
            outcome(op, true, client.check(check).await)
        }
        Operation::CheckBulk => {
            let check = PermissionCheck::new(resource, "view", subject, full());
            let request = BulkCheckRequest::new(vec![check]);
            outcome(op, true, client.check_bulk(request).await)
        }
        Operation::Write => {
            let mut txn = Transaction::new();
            txn.touch(&rel);
            outcome(op, true, client.write(WriteRequest::new(txn)).await)
        }
        Operation::ReadRelationships => {
            let request = ReadRelationshipsRequest::new(Filter::new("document"), full());
            outcome(op, true, first(client.read_relationships(request)).await)
        }
        Operation::DeleteRelationships => {
            let request = DeleteRequest::new(Filter::new("folder"), one);
            outcome(op, true, client.delete_relationships(request).await)
        }
        Operation::DeleteAllRelationshipsNonAtomic => {
            // Its pages are DeleteRelationships RPCs.
            let request = DeleteAllRequest::new(Filter::new("folder"));
            let result = client.delete_all_relationships_non_atomic(request).await;
            outcome(Operation::DeleteRelationships, true, result)
        }
        Operation::LookupResources => {
            let request = LookupResourcesRequest::new("document", "view", subject, full());
            outcome(op, true, first(client.lookup_resources(request)).await)
        }
        Operation::LookupSubjects => {
            let request = LookupSubjectsRequest::new(resource, "view", "user", full());
            outcome(op, true, first(client.lookup_subjects(request)).await)
        }
        Operation::ExpandPermissionTree => {
            let request = ExpandPermissionTreeRequest::new(resource, "view", full());
            outcome(op, false, client.expand_permission_tree(request).await)
        }
        Operation::ImportRelationships => {
            let request = ImportRelationshipsRequest::new(futures::stream::iter([Ok(rel)]));
            outcome(op, true, client.import_relationships(request).await)
        }
        Operation::ExportRelationships => {
            let request = ExportRelationshipsRequest::new(full());
            outcome(op, true, first(client.export_relationships(request)).await)
        }
        Operation::Updates => {
            let request = UpdatesRequest::new(vec!["document".into()]);
            outcome(op, false, first(client.updates(request)).await)
        }
        Operation::ReadSchema => {
            outcome(op, true, client.read_schema(ReadSchemaRequest::new()).await)
        }
        Operation::WriteSchema => {
            let request = WriteSchemaRequest::new("definition user {}");
            outcome(op, true, client.write_schema(request).await)
        }
        Operation::ReflectSchema => {
            let request = ReflectSchemaRequest::new(full());
            outcome(op, true, client.reflect_schema(request).await)
        }
        Operation::ComputablePermissions => {
            let request = ComputablePermissionsRequest::new("document", "viewer", full());
            outcome(op, false, client.computable_permissions(request).await)
        }
        Operation::DependentRelations => {
            let request = DependentRelationsRequest::new("document", "view", full());
            outcome(op, false, client.dependent_relations(request).await)
        }
        Operation::DiffSchema => {
            let request = DiffSchemaRequest::new("definition user {}", full());
            outcome(op, false, client.diff_schema(request).await)
        }
        Operation::ExperimentalRegisterRelationshipCounter => {
            let request = ExperimentalRegisterRelationshipCounterRequest::new(
                "docs",
                Filter::new("document"),
            );
            let result = client
                .experimental_register_relationship_counter(request)
                .await;
            outcome(op, false, result)
        }
        Operation::ExperimentalCountRelationships => {
            let request = ExperimentalCountRelationshipsRequest::new("docs");
            outcome(
                op,
                false,
                client.experimental_count_relationships(request).await,
            )
        }
        Operation::ExperimentalUnregisterRelationshipCounter => {
            let request = ExperimentalUnregisterRelationshipCounterRequest::new("docs");
            let result = client
                .experimental_unregister_relationship_counter(request)
                .await;
            outcome(op, false, result)
        }
        Operation::ExperimentalCountRelationshipsByFilter => {
            let request =
                ExperimentalCountRelationshipsByFilterRequest::new(Filter::new("document"));
            let result = client
                .experimental_count_relationships_by_filter(request)
                .await;
            outcome(op, false, result)
        }
        Operation::ExperimentalRoaringLookupResources => {
            let request =
                ExperimentalRoaringLookupResourcesRequest::new("document", "view", subject, full());
            let result = client.experimental_roaring_lookup_resources(request).await;
            outcome(op, false, result)
        }
        Operation::WatchPermissionSets => {
            let request = WatchPermissionSetsRequest::new();
            outcome(
                op,
                false,
                first(client.watch_permission_sets(request)).await,
            )
        }
        Operation::LookupPermissionSets => {
            let request = LookupPermissionSetsRequest::new(one);
            outcome(
                op,
                false,
                first(client.lookup_permission_sets(request)).await,
            )
        }
        Operation::DownloadPermissionSets => {
            let request = DownloadPermissionSetsRequest::new();
            outcome(op, false, client.download_permission_sets(request).await)
        }
    }
}

#[test]
fn every_operation_is_listed_once() {
    for (i, operation) in ALL.iter().enumerate() {
        assert_eq!(ordinal(*operation), i, "{operation} is listed twice");
    }
}

#[tokio::test]
async fn every_client_method_reaches_the_double() {
    for operation in ALL {
        let fake = InMemorySpiceDB::new();
        let client = fake.client().unwrap();
        let Outcome {
            recorded,
            modeled,
            result,
        } = exercise(&client, operation).await;
        assert_eq!(
            fake.calls_of(recorded).len(),
            1,
            "{operation}: {:?}",
            fake.calls()
        );
        match result {
            Err(e) if !modeled => {
                // Mapped by the real client, like SpiceDB's own status.
                assert_eq!(e.code(), Code::Unimplemented, "{operation}: {e:?}");
                assert!(
                    e.server_message()
                        .is_some_and(|m| m.contains("not modeled")),
                    "{operation}: {e:?}"
                );
            }
            Ok(()) if !modeled => panic!("{operation} succeeded but is not modeled"),
            Err(e) => assert_ne!(e.code(), Code::Unimplemented, "{operation}: {e:?}"),
            Ok(()) => {}
        }
    }
}

#[tokio::test]
async fn every_client_method_consults_the_fault_hook() {
    for operation in ALL {
        let fake = InMemorySpiceDB::new();
        fake.set_fault_hook(|fault| {
            (fault.stage == FaultStage::Call)
                .then(|| server_error(Code::PermissionDenied, "injected"))
        });
        let client = fake.client().unwrap();
        let err = match exercise(&client, operation).await.result {
            Err(e) => e,
            Ok(()) => panic!("{operation} ignored the fault hook"),
        };
        assert_eq!(err.code(), Code::PermissionDenied, "{operation}: {err:?}");
        assert_eq!(err.server_message(), Some("injected"), "{operation}");
    }
}
