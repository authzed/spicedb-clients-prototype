// Stub descriptors for transitive proto dependencies referenced in buf-generated
// descriptor registrations (protovalidate, protoc-gen-validate and the openapiv2
// annotations). The SpiceDB protos use them only as custom options, so no generated
// type needs their contents. Each stub is an otherwise empty file descriptor carrying
// the dependency's real file name and package: the descriptor pool keys files by name,
// so stubs without names collide and every dependent descriptor fails to initialise.
// They avoid the need for BSR NuGet auth or additional NuGet packages that don't exist
// on nuget.org.

namespace Authzed.Api.SpiceDB.Proto
{
    using Google.Protobuf;
    using Google.Protobuf.Reflection;

    internal static class TransitiveProtoStub
    {
        internal static FileDescriptor Build(string name, string package, params FileDescriptor[] dependencies)
        {
            var proto = new FileDescriptorProto { Name = name, Package = package, Syntax = "proto3" };
            foreach (var dependency in dependencies)
            {
                proto.Dependency.Add(dependency.Name);
            }
            return FileDescriptor.FromGeneratedCode(
                proto.ToByteArray(),
                dependencies,
                new GeneratedClrTypeInfo(null, null, null));
        }
    }
}

namespace Buf.Validate
{
    internal static class ValidateReflection
    {
        internal static Google.Protobuf.Reflection.FileDescriptor Descriptor { get; } =
            Authzed.Api.SpiceDB.Proto.TransitiveProtoStub.Build("buf/validate/validate.proto", "buf.validate");
    }
}

namespace Grpc.Gateway.ProtocGenOpenapiv2.Options
{
    internal static class Openapiv2Reflection
    {
        internal static Google.Protobuf.Reflection.FileDescriptor Descriptor { get; } =
            Authzed.Api.SpiceDB.Proto.TransitiveProtoStub.Build(
                "protoc-gen-openapiv2/options/openapiv2.proto", "grpc.gateway.protoc_gen_openapiv2.options");
    }

    internal static class AnnotationsReflection
    {
        internal static Google.Protobuf.Reflection.FileDescriptor Descriptor { get; } =
            Authzed.Api.SpiceDB.Proto.TransitiveProtoStub.Build(
                "protoc-gen-openapiv2/options/annotations.proto", "grpc.gateway.protoc_gen_openapiv2.options",
                Openapiv2Reflection.Descriptor);
    }
}

namespace Validate
{
    internal static class ValidateReflection
    {
        internal static Google.Protobuf.Reflection.FileDescriptor Descriptor { get; } =
            Authzed.Api.SpiceDB.Proto.TransitiveProtoStub.Build("validate/validate.proto", "validate");
    }
}
