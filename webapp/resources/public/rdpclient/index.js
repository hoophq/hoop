import { RemoteDesktopService, Config, ReplayConfig } from './rdp.js';
import * as wasmModule from "./pkg/ironrdp_web_bg.js";
import { __wbg_set_wasm } from "./pkg/ironrdp_web_bg.js";

let wasmInstance;

async function initWasm() {
    try {
        const response = await fetch('/rdpclient/pkg/ironrdp_web_bg.wasm');
        const buffer = await response.arrayBuffer();

        // Get the imports from the wasm module
        const imports = wasmModule;

        // Instantiate with imports
        const { instance } = await WebAssembly.instantiate(buffer, {
            "./ironrdp_web_bg.js": imports
        });

        __wbg_set_wasm(instance.exports);
        instance.exports.__wbindgen_start?.();
        wasmInstance = wasmModule;
        return wasmModule;
    } catch (err) {
        console.error('Failed to load WASM:', err);
        throw err;
    }
}

async function initializeApp(rdpCredential) {
    console.log('Initializing app...');
    const mod = await initWasm();
    console.log('App initialized.');
    const rdpCanvas = document.getElementById('rdp-canvas');
    // Set rdpCanvas to fill screen
    rdpCanvas.style.width = '100%';
    rdpCanvas.style.height = '100%';
    rdpCanvas.style.margin = '0px';
    rdpCanvas.style.position = 'absolute';
    rdpCanvas.style.top = '0px';
    rdpCanvas.style.left = '0px';
    document.body.style.margin = '0px';
    document.body.overflow = 'hidden';
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    console.log(`Window Size: ${viewportWidth} x ${viewportHeight}`);
    rdpCanvas.width = viewportWidth;
    rdpCanvas.height = viewportHeight;

    const RDP = new RemoteDesktopService(rdpCanvas, mod);
    window.RDP = RDP;

    // Check if we're in replay mode (session_id in URL params or path)
    const urlParams = new URLSearchParams(window.location.search);
    let sessionId = urlParams.get('session_id');

    // Also check if session_id is in the URL path (e.g., /rdpproxy/replay-client/<session_id>)
    if (!sessionId) {
        const pathParts = window.location.pathname.split('/');
        // Check for /rdpproxy/replay-client/<session_id>
        const replayClientIndex = pathParts.indexOf('replay-client');
        if (replayClientIndex !== -1 && pathParts.length > replayClientIndex + 1) {
            sessionId = pathParts[replayClientIndex + 1];
        }
    }

    if (sessionId) {
        // Replay mode - connect to replay endpoint
        const replayAddr = (window.location.protocol === 'https:') ?
            'wss://' + window.location.host + "/rdpproxy/replay/" + sessionId
            :
            'ws://' + window.location.host + "/rdpproxy/replay/" + sessionId
        ;

        console.log('Replay mode: connecting to', replayAddr);
        RDP.clearScreenAndWriteText("Loading replay...");

        try {
            const result = await RDP.replay(new ReplayConfig(replayAddr));
            await result.run();
        } catch (err) {
            RDP.reportError(err);
        }
    } else {
        // Live connection mode
        const proxyAddr = (window.location.protocol === 'https:') ?
            'wss://' + window.location.host + "/rdpproxy/"
            :
            'ws://' + window.location.host + "/rdpproxy/"
        ;
        const rdpConfig = new Config(
            rdpCredential,
            rdpCredential,
            proxyAddr,
        )

        console.log('Connecting...')
        RDP.clearScreenAndWriteText("Connecting...");
        try {
            const result = await RDP.connect(rdpConfig);
            await result.run();
        } catch (err) {
            RDP.reportError(err);
        }
    }
}

window.initializeApp = initializeApp;
