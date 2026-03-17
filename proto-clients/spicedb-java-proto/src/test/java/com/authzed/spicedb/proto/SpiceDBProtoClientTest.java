package com.authzed.spicedb.proto;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class SpiceDBProtoClientTest {

    @Test
    void constructorCreatesClientWithAllStubs() {
        try (SpiceDBProtoClient client = new SpiceDBProtoClient("localhost:50051", "test-token", true)) {
            assertNotNull(client.permissions(), "permissions stub should not be null");
            assertNotNull(client.schema(), "schema stub should not be null");
            assertNotNull(client.watch(), "watch stub should not be null");
            assertNotNull(client.experimental(), "experimental stub should not be null");
        }
    }

    @Test
    void channelIsAccessible() {
        try (SpiceDBProtoClient client = new SpiceDBProtoClient("localhost:50051", "test-token", true)) {
            assertNotNull(client.getChannel(), "channel should not be null");
            assertFalse(client.getChannel().isShutdown(), "channel should not be shut down");
        }
    }

    @Test
    void closeShutdownsChannel() {
        SpiceDBProtoClient client = new SpiceDBProtoClient("localhost:50051", "test-token", true);
        client.close();
        assertTrue(client.getChannel().isShutdown(), "channel should be shut down after close");
    }
}
