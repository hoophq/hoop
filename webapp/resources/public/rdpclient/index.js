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
    document.body.overflow = 'hidden';
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    console.log(`Window Size: ${viewportWidth} x ${viewportHeight}`);
    rdpCanvas.width = viewportWidth;
    rdpCanvas.height = viewportHeight;
    
    const RDP = new RemoteDesktopService(rdpCanvas, mod);
    window.RDP = RDP;
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
