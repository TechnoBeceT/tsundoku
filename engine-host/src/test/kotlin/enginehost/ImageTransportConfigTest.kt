package enginehost

import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotSame
import kotlin.test.assertSame
import okhttp3.OkHttpClient

class ImageTransportConfigTest {
    @AfterTest
    fun clearImageTransport() {
        ConfigPush.applyImageTransport(ImageTransportConfigRequest(reuseSourceIds = emptyList()))
    }

    @Test
    fun `image transport normalizes duplicate source IDs into a sorted immutable selection`() {
        ConfigPush.applyImageTransport(
            ImageTransportConfigRequest(reuseSourceIds = listOf(9L, 3L, 9L, 1L)),
        )

        assertEquals(listOf(1L, 3L, 9L), ConfigPush.readImageTransport().reuseSourceIds)
    }

    @Test
    fun `an omitted image transport list preserves the current selection`() {
        ConfigPush.applyImageTransport(ImageTransportConfigRequest(reuseSourceIds = listOf(9L)))

        ConfigPush.applyImageTransport(ImageTransportConfigRequest())

        assertEquals(listOf(9L), ConfigPush.readImageTransport().reuseSourceIds)
    }

    @Test
    fun `an explicit empty image transport list clears the current selection`() {
        ConfigPush.applyImageTransport(ImageTransportConfigRequest(reuseSourceIds = listOf(9L)))

        ConfigPush.applyImageTransport(ImageTransportConfigRequest(reuseSourceIds = emptyList()))

        assertEquals(emptyList(), ConfigPush.readImageTransport().reuseSourceIds)
    }

    @Test
    fun `selected sources reuse the normal pooled source client`() {
        ConfigPush.applyImageTransport(ImageTransportConfigRequest(reuseSourceIds = listOf(9L)))
        val sourceClient = OkHttpClient()

        val selected = SourceCalls.imageClientFor(9L, sourceClient)

        assertSame(sourceClient, selected)
    }

    @Test
    fun `unselected sources receive a fresh no-idle-pool client`() {
        ConfigPush.applyImageTransport(ImageTransportConfigRequest(reuseSourceIds = listOf(9L)))
        val sourceClient = OkHttpClient()

        val selected = SourceCalls.imageClientFor(8L, sourceClient)
        val nextSelected = SourceCalls.imageClientFor(8L, sourceClient)

        assertNotSame(sourceClient, selected)
        assertNotSame(sourceClient.connectionPool, selected.connectionPool)
        assertNotSame(selected.connectionPool, nextSelected.connectionPool)
        assertEquals(0, selected.connectionPool.idleConnectionCount())
    }
}
