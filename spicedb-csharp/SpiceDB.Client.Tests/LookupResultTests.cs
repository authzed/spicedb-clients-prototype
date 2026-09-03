// Tests for the native lookup result types (LookupResource, LookupSubject,
// ResolvedSubject, PartialCaveatInfo, Permissionship) and the internal
// proto -> native mappers (SpiceDBClient.ToPermissionship/
// ToPartialCaveatInfo/ToResolvedSubject/ToLookupSubject) that back
// LookupResourcesAsync/LookupSubjectsAsync. These replace bare string yields
// — critically, ensuring wildcard "*" subjects surface their
// ExcludedSubjects instead of silently dropping them (an over-grant risk).

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using SpiceDB.Client;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class LookupResultTests
{
    // ── ToPermissionship ─────────────────────────────────────────────────

    [Theory]
    [InlineData(LookupPermissionship.Unspecified, Permissionship.Unspecified)]
    [InlineData(LookupPermissionship.HasPermission, Permissionship.HasPermission)]
    [InlineData(LookupPermissionship.ConditionalPermission, Permissionship.ConditionalPermission)]
    public void ToPermissionship_MapsKnownValues(LookupPermissionship proto, Permissionship expected)
    {
        SpiceDBClient.ToPermissionship(proto).Should().Be(expected);
    }

    [Fact]
    public void ToPermissionship_MapsUnrecognizedValueToUnspecified()
    {
        SpiceDBClient.ToPermissionship((LookupPermissionship)99).Should().Be(Permissionship.Unspecified);
    }

    // ── ToPartialCaveatInfo ──────────────────────────────────────────────

    [Fact]
    public void ToPartialCaveatInfo_NullInputMapsToNull()
    {
        SpiceDBClient.ToPartialCaveatInfo(null).Should().BeNull();
    }

    [Fact]
    public void ToPartialCaveatInfo_MapsMissingRequiredContext()
    {
        var proto = new Authzed.Api.V1.PartialCaveatInfo
        {
            MissingRequiredContext = { "region", "role" },
        };

        var result = SpiceDBClient.ToPartialCaveatInfo(proto);

        result.Should().NotBeNull();
        result!.MissingRequiredContext.Should().Equal("region", "role");
    }

    // ── ToResolvedSubject ────────────────────────────────────────────────

    [Fact]
    public void ToResolvedSubject_NullInputMapsToZeroValue()
    {
        var result = SpiceDBClient.ToResolvedSubject(null);

        result.SubjectID.Should().BeEmpty();
        result.Permissionship.Should().Be(Permissionship.Unspecified);
        result.PartialCaveat.Should().BeNull();
    }

    [Fact]
    public void ToResolvedSubject_MapsAllFields()
    {
        var proto = new Authzed.Api.V1.ResolvedSubject
        {
            SubjectObjectId = "alice",
            Permissionship = LookupPermissionship.ConditionalPermission,
            PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo { MissingRequiredContext = { "ip" } },
        };

        var result = SpiceDBClient.ToResolvedSubject(proto);

        result.SubjectID.Should().Be("alice");
        result.Permissionship.Should().Be(Permissionship.ConditionalPermission);
        result.PartialCaveat.Should().NotBeNull();
        result.PartialCaveat!.MissingRequiredContext.Should().Equal("ip");
    }

    // ── ToLookupSubject: wildcard excluded subjects (the over-grant fix) ───

    [Fact]
    public void ToLookupSubject_WildcardSubject_SurfacesExcludedSubjects()
    {
        var resp = new LookupSubjectsResponse
        {
            Subject = new Authzed.Api.V1.ResolvedSubject
            {
                SubjectObjectId = "*",
                Permissionship = LookupPermissionship.HasPermission,
            },
            ExcludedSubjects =
            {
                new Authzed.Api.V1.ResolvedSubject
                {
                    SubjectObjectId = "alice",
                    Permissionship = LookupPermissionship.HasPermission,
                },
                new Authzed.Api.V1.ResolvedSubject
                {
                    SubjectObjectId = "bob",
                    Permissionship = LookupPermissionship.ConditionalPermission,
                    PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo { MissingRequiredContext = { "region" } },
                },
            },
        };

        var result = SpiceDBClient.ToLookupSubject(resp);

        result.Subject.SubjectID.Should().Be("*");
        result.Subject.Permissionship.Should().Be(Permissionship.HasPermission);

        // Critical: both excluded subjects must be surfaced, not dropped —
        // otherwise callers would treat the wildcard as a blanket grant and
        // over-grant access to alice and bob.
        result.ExcludedSubjects.Should().HaveCount(2);

        result.ExcludedSubjects[0].SubjectID.Should().Be("alice");
        result.ExcludedSubjects[0].Permissionship.Should().Be(Permissionship.HasPermission);
        result.ExcludedSubjects[0].PartialCaveat.Should().BeNull();

        // HAS vs CONDITIONAL must be distinguished per excluded subject too.
        result.ExcludedSubjects[1].SubjectID.Should().Be("bob");
        result.ExcludedSubjects[1].Permissionship.Should().Be(Permissionship.ConditionalPermission);
        result.ExcludedSubjects[1].PartialCaveat.Should().NotBeNull();
        result.ExcludedSubjects[1].PartialCaveat!.MissingRequiredContext.Should().Equal("region");
    }

    [Fact]
    public void ToLookupSubject_NoExclusions_ReturnsEmptyExcludedSubjects()
    {
        var resp = new LookupSubjectsResponse
        {
            Subject = new Authzed.Api.V1.ResolvedSubject
            {
                SubjectObjectId = "alice",
                Permissionship = LookupPermissionship.HasPermission,
            },
        };

        var result = SpiceDBClient.ToLookupSubject(resp);

        result.ExcludedSubjects.Should().BeEmpty();
    }

    // ── ToLookupSubject: deprecated field fallbacks ─────────────────────────

    [Fact]
    public void ToLookupSubject_FallsBackToDeprecatedSubjectFields_WhenSubjectUnset()
    {
        var resp = new LookupSubjectsResponse();
#pragma warning disable CS0612 // exercising the deprecated-field fallback path
        resp.SubjectObjectId = "alice";
        resp.Permissionship = LookupPermissionship.ConditionalPermission;
        resp.PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo { MissingRequiredContext = { "role" } };
#pragma warning restore CS0612

        var result = SpiceDBClient.ToLookupSubject(resp);

        result.Subject.SubjectID.Should().Be("alice");
        result.Subject.Permissionship.Should().Be(Permissionship.ConditionalPermission);
        result.Subject.PartialCaveat.Should().NotBeNull();
        result.Subject.PartialCaveat!.MissingRequiredContext.Should().Equal("role");
    }

    [Fact]
    public void ToLookupSubject_PrefersNonDeprecatedSubjectField_WhenBothSet()
    {
        var resp = new LookupSubjectsResponse
        {
            Subject = new Authzed.Api.V1.ResolvedSubject
            {
                SubjectObjectId = "alice",
                Permissionship = LookupPermissionship.HasPermission,
            },
        };
#pragma warning disable CS0612 // proving the deprecated field is ignored when `subject` is set
        resp.SubjectObjectId = "someone-else";
        resp.Permissionship = LookupPermissionship.ConditionalPermission;
#pragma warning restore CS0612

        var result = SpiceDBClient.ToLookupSubject(resp);

        result.Subject.SubjectID.Should().Be("alice");
        result.Subject.Permissionship.Should().Be(Permissionship.HasPermission);
    }

    [Fact]
    public void ToLookupSubject_FallsBackToDeprecatedExcludedSubjectIds_WhenExcludedSubjectsUnset()
    {
        var resp = new LookupSubjectsResponse
        {
            Subject = new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "*" },
        };
#pragma warning disable CS0612 // exercising the deprecated-field fallback path
        resp.ExcludedSubjectIds.Add("carol");
        resp.ExcludedSubjectIds.Add("dave");
#pragma warning restore CS0612

        var result = SpiceDBClient.ToLookupSubject(resp);

        // The deprecated field carries only IDs — no permissionship/caveat info.
        result.ExcludedSubjects.Should().HaveCount(2);
        result.ExcludedSubjects[0].SubjectID.Should().Be("carol");
        result.ExcludedSubjects[0].Permissionship.Should().Be(Permissionship.Unspecified);
        result.ExcludedSubjects[1].SubjectID.Should().Be("dave");
    }

    [Fact]
    public void ToLookupSubject_PrefersNonDeprecatedExcludedSubjects_WhenBothSet()
    {
        var resp = new LookupSubjectsResponse
        {
            Subject = new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "*" },
            ExcludedSubjects = { new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "alice" } },
        };
#pragma warning disable CS0612 // proving the deprecated field is ignored when `excluded_subjects` is set
        resp.ExcludedSubjectIds.Add("someone-else");
#pragma warning restore CS0612

        var result = SpiceDBClient.ToLookupSubject(resp);

        result.ExcludedSubjects.Should().ContainSingle();
        result.ExcludedSubjects[0].SubjectID.Should().Be("alice");
    }

    // ── End-to-end: LookupSubjectsAsync surfaces wildcard exclusions ────────
    // Exercises the full public API (not just the mapper in isolation) using
    // the same Moq'd gRPC client seam as StreamingErrorMappingTests.

    [Fact]
    public async Task LookupSubjectsAsync_WildcardWithExclusions_SurfacesThroughPublicApi()
    {
        var resp = new LookupSubjectsResponse
        {
            Subject = new Authzed.Api.V1.ResolvedSubject
            {
                SubjectObjectId = "*",
                Permissionship = LookupPermissionship.HasPermission,
            },
            ExcludedSubjects =
            {
                new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "alice", Permissionship = LookupPermissionship.HasPermission },
                new Authzed.Api.V1.ResolvedSubject
                {
                    SubjectObjectId = "bob",
                    Permissionship = LookupPermissionship.ConditionalPermission,
                    PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo { MissingRequiredContext = { "region" } },
                },
            },
        };

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupSubjects(
                It.IsAny<LookupSubjectsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<LookupSubjectsResponse>([resp])));

        await using var client = new SpiceDBClient(
            mockPermissions.Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

        var results = new List<LookupSubject>();
        await foreach (var result in client.LookupSubjectsAsync(
            Consistency.Full(), "document", "doc1", "view", "user"))
        {
            results.Add(result);
        }

        results.Should().ContainSingle();
        var lookupSubject = results[0];
        lookupSubject.Subject.SubjectID.Should().Be("*");
        lookupSubject.ExcludedSubjects.Should().HaveCount(2);
        lookupSubject.ExcludedSubjects.Select(s => s.SubjectID).Should().Equal("alice", "bob");
        lookupSubject.ExcludedSubjects[1].Permissionship.Should().Be(Permissionship.ConditionalPermission);
    }

    // ── End-to-end: LookupResourcesAsync surfaces permissionship/caveat ────

    [Fact]
    public async Task LookupResourcesAsync_SurfacesPermissionshipAndPartialCaveat()
    {
        var resp = new LookupResourcesResponse
        {
            ResourceObjectId = "doc1",
            Permissionship = LookupPermissionship.ConditionalPermission,
            PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo { MissingRequiredContext = { "ip" } },
        };

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>([resp])));

        await using var client = new SpiceDBClient(
            mockPermissions.Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

        var results = new List<LookupResource>();
        await foreach (var result in client.LookupResourcesAsync(
            Consistency.Full(), "document", "view", "user", "alice"))
        {
            results.Add(result);
        }

        results.Should().ContainSingle();
        results[0].ResourceID.Should().Be("doc1");
        results[0].Permissionship.Should().Be(Permissionship.ConditionalPermission);
        results[0].PartialCaveat.Should().NotBeNull();
        results[0].PartialCaveat!.MissingRequiredContext.Should().Equal("ip");
    }

    [Fact]
    public async Task LookupResourcesAsync_WithDebugTrue_SetsWithDebugOnRequest()
    {
        LookupResourcesRequest? capturedRequest = null;

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Callback<LookupResourcesRequest, Metadata, DateTime?, CancellationToken>(
                (req, _, _, _) => capturedRequest = req)
            .Returns(MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>([])));

        await using var client = new SpiceDBClient(
            mockPermissions.Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

        await foreach (var _ in client.LookupResourcesAsync(
            Consistency.Full(), "document", "view", "user", "alice", withDebug: true))
        {
        }

        capturedRequest.Should().NotBeNull();
        capturedRequest!.WithDebug.Should().BeTrue();
    }

    [Fact]
    public async Task LookupResourcesAsync_WithDebugOmitted_DefaultsToFalseOnRequest()
    {
        LookupResourcesRequest? capturedRequest = null;

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Callback<LookupResourcesRequest, Metadata, DateTime?, CancellationToken>(
                (req, _, _, _) => capturedRequest = req)
            .Returns(MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>([])));

        await using var client = new SpiceDBClient(
            mockPermissions.Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

        await foreach (var _ in client.LookupResourcesAsync(
            Consistency.Full(), "document", "view", "user", "alice"))
        {
        }

        capturedRequest.Should().NotBeNull();
        capturedRequest!.WithDebug.Should().BeFalse();
    }
}
