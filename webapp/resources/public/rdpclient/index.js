import { RemoteDesktopService, Config } from './rdp.js';
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
    document.body.style.overflow = 'hidden';

    // Size the canvas backing store in physical (device) pixels so the RDP
    // desktop maps 1:1 to the screen. Using CSS pixels (window.innerWidth)
    // on a HiDPI/Retina display forces the browser to upscale the canvas,
    // blurring the whole session.
    //
    // SECURITY CONTRACT: MAX_DESKTOP_DIM must never exceed the PII guard's
    // shadow canvas bound (maxCanvasDim in gateway/rdp/piigate.go and
    // MAX_CANVAS_DIM in agentrs/src/piigate/canvas.rs, both 4096). Beyond
    // that bound the guard skips redaction FAIL-OPEN, so oversized displays
    // are scaled down proportionally instead. Keep the three values in
    // lockstep.
    //
    // This clamp is best-effort: it bounds the size *requested* by this
    // client, but the server may still negotiate a different desktop size.
    // Authoritative enforcement at the gateway/agent boundary is tracked in
    // DEP-70.
    const MAX_DESKTOP_DIM = 4096;
    const viewportWidth = Number.isFinite(window.innerWidth)
        ? Math.max(1, Math.floor(window.innerWidth)) : 1;
    const viewportHeight = Number.isFinite(window.innerHeight)
        ? Math.max(1, Math.floor(window.innerHeight)) : 1;
    const dpr = (Number.isFinite(window.devicePixelRatio) && window.devicePixelRatio > 0)
        ? window.devicePixelRatio : 1;
    const scale = Math.min(
        dpr,
        MAX_DESKTOP_DIM / viewportWidth,
        MAX_DESKTOP_DIM / viewportHeight,
    );
    console.log(`Window Size: ${viewportWidth} x ${viewportHeight}, DPR: ${dpr}, scale: ${scale}`);
    rdpCanvas.width = Math.max(1, Math.min(MAX_DESKTOP_DIM, Math.round(viewportWidth * scale)));
    rdpCanvas.height = Math.max(1, Math.min(MAX_DESKTOP_DIM, Math.round(viewportHeight * scale)));

    const RDP = new RemoteDesktopService(rdpCanvas, mod);
    window.RDP = RDP;

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

window.initializeApp = initializeApp;
