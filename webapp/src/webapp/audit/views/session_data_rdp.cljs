(ns webapp.audit.views.session-data-rdp
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text Heading Badge]]
   ["lucide-react" :refer [Play Pause SkipBack SkipForward Maximize Minimize]]
   [reagent.core :as r]
   [webapp.audit.views.empty-event-stream :as empty-event-stream]
   [webapp.components.loaders :as loaders]
   [webapp.http.api :as api]))

;; Add fullscreen styles
(defonce ^:private fullscreen-styles-added (atom false))

(defn- ensure-fullscreen-styles []
  (when-not @fullscreen-styles-added
    (let [style-el (js/document.createElement "style")]
      (set! (.-innerHTML style-el)
        ".rdp-player-container:fullscreen {
          height: 100vh;
          width: 100vw;
          background-color: black;
        }
        .rdp-player-container:fullscreen .rdp-canvas-container {
          height: calc(100vh - 100px);
        }
        .rdp-player-container:fullscreen canvas {
          max-height: 100%;
          max-width: 100%;
        }")
      (.appendChild js/document.head style-el)
      (reset! fullscreen-styles-added true))))

(defn- decode-b64 [b64-str]
  (try
    (let [binary-string (js/atob b64-str)
          len (.-length binary-string)
          bytes (js/Uint8Array. len)]
      (doseq [i (range len)]
        (aset bytes i (.charCodeAt binary-string i)))
      bytes)
    (catch :default _
      nil)))

;; Load the RLE decompressor script on first use
(defonce ^:private rle-loaded (atom false))

(defn- ensure-rle-loaded []
  (when-not @rle-loaded
    (let [script (js/document.createElement "script")]
      (set! (.-src script) "/rdpclient/rle.js")
      (.appendChild js/document.head script)
      (reset! rle-loaded true))))

(defn- decompress-rle
  "Decompress RLE-compressed RDP bitmap data using the rle.js library"
  [data-bytes width height bpp]
  (if-let [decompress-fn js/window.rdpRleDecompress]
    (try
      (decompress-fn data-bytes width height bpp)
      (catch :default e
        (js/console.error "RLE decompression failed:" e)
        data-bytes))
    (do
      (js/console.warn "RLE decompressor not loaded, using raw data")
      data-bytes)))

(defn- draw-bitmap [canvas-ctx bitmap]
  (let [{:keys [x y width height bits_per_pixel compressed data]} bitmap
        data-bytes (if (string? data) (decode-b64 data) data)]
    (when (and canvas-ctx data-bytes (> width 0) (> height 0))
      (try
        (js/console.time "draw-bitmap")  ;; ← adiciona
        (let [bpp (or bits_per_pixel 16)
              ;; Decompress if needed
              pixel-data (if compressed
                           (decompress-rle data-bytes width height bpp)
                           data-bytes)
              ;; Use fast JS pixel conversion (handles bottom-up + BGR->RGB)
              rgba (js/window.rdpBitmapToRGBA pixel-data width height bpp)
              image-data (js/ImageData. rgba width height)]
          (.putImageData canvas-ctx image-data x y))
        (js/console.timeEnd "draw-bitmap")  ;; ← e esse
        (catch :default e
          (js/console.error "Error drawing bitmap:" e))))))

;; Fetch frames from API
(defn- fetch-frames [session-id offset limit on-success on-error]
  (api/request {:method "GET"
                :uri (str "/sessions/" session-id "/rdp-frames")
                :query-params {:offset offset :limit limit}
                :on-success on-success
                :on-failure on-error}))

;; Format time as MM:SS
(defn- format-time [seconds]
  (let [mins (js/Math.floor (/ seconds 60))
        secs (js/Math.floor (mod seconds 60))]
    (str (when (< mins 10) "0") mins ":" (when (< secs 10) "0") secs)))

;; Progress bar with seek functionality (timestamp-based)
(defn- progress-bar [{:keys [current-time total-duration on-seek]}]
  (let [progress (if (> total-duration 0) (* 100 (/ current-time total-duration)) 0)]
    [:> Box {:class "w-full bg-[--gray-7] rounded-full h-2 cursor-pointer"
             :on-click (fn [e]
                         (let [rect (.. e -target getBoundingClientRect)
                               x (- (.-clientX e) (.-left rect))
                               width (.-width rect)
                               percentage (/ x width)
                               target-time (* percentage total-duration)]
                           (on-seek (max 0 target-time))))}
     [:div {:class "bg-[--accent-9] h-2 rounded-full transition-all duration-100"
            :style {:width (str progress "%")}}]]))

;; Playback controls
(defn- playback-controls [{:keys [playing? on-play on-pause on-prev on-next
                                  current-time total-duration on-seek playback-speed on-speed-change
                                  on-fullscreen fullscreen?]}]
  [:> Box {:class "space-y-radix-2 p-radix-4 bg-[--gray-2] rounded-lg"}
   [progress-bar {:current-time current-time
                  :total-duration total-duration
                  :on-seek on-seek}]
   [:> Flex {:justify "between" :align "center"}
    [:> Flex {:gap "2" :align "center"}
     [:> Button {:variant "soft"
                 :size "2"
                 :color "gray"
                 :on-click on-prev}
      [:> SkipBack {:size 16}]]
     [:> Button {:variant "solid"
                 :size "3"
                 :on-click (if playing? on-pause on-play)}
      (if playing?
        [:> Flex {:gap "2" :align "center"}
         [:> Pause {:size 16}]
         "Pause"]
        [:> Flex {:gap "2" :align "center"}
         [:> Play {:size 16}]
         "Play"])]
     [:> Button {:variant "soft"
                 :size "2"
                 :color "gray"
                 :on-click on-next}
      [:> SkipForward {:size 16}]]]
    [:> Flex {:gap "4" :align "center"}
     [:> Text {:size "2" :class "text-[--gray-11]"}
      (str (format-time current-time) " / " (format-time total-duration))]
     [:> Flex {:gap "1" :align "center"}
      [:> Button {:variant (if (= playback-speed 0.5) "solid" "soft")
                  :size "1"
                  :color "gray"
                  :on-click #(on-speed-change 0.5)}
       "0.5x"]
      [:> Button {:variant (if (= playback-speed 1) "solid" "soft")
                  :size "1"
                  :color "gray"
                  :on-click #(on-speed-change 1)}
       "1x"]
      [:> Button {:variant (if (= playback-speed 2) "solid" "soft")
                  :size "1"
                  :color "gray"
                  :on-click #(on-speed-change 2)}
       "2x"]
      [:> Button {:variant (if (= playback-speed 4) "solid" "soft")
                  :size "1"
                  :color "gray"
                  :on-click #(on-speed-change 4)}
       "4x"]]
     [:> Button {:variant "soft"
                 :size "2"
                 :color "gray"
                 :on-click on-fullscreen}
      (if fullscreen?
        [:> Minimize {:size 16}]
        [:> Maximize {:size 16}])]]]])

;; Canvas component that renders frames differentially
(defn- canvas-renderer []
  (let [canvas-ref (atom nil)
        ;; Track last rendered frame index to know what's new
        last-rendered-idx (atom -1)]
    (r/create-class
     {:display-name "rdp-canvas-renderer"
      :component-did-update
      (fn [this]
        (let [[_ frames canvas-width canvas-height current-frame-idx] (r/argv this)
              canvas @canvas-ref]
          (when (and canvas (seq frames)
                     ;; Only redraw when frame index actually changed
                     (not= current-frame-idx @last-rendered-idx))
            (let [ctx (.getContext canvas "2d" (clj->js {:alpha false
                                                         :desynchronized true
                                                         :willReadFrequently false}))]
              ;; Disable image smoothing for better performance
              (set! (.-imageSmoothingEnabled ctx) false)
              ;; If seeking backwards or first render, clear and redraw from beginning
              (if (or (< current-frame-idx @last-rendered-idx)
                      (= @last-rendered-idx -1))
                (do
                  ;; Clear canvas
                  (set! (.-fillStyle ctx) "black")
                  (.fillRect ctx 0 0 canvas-width canvas-height)
                  ;; Draw all frames up to current
                  (doseq [frame (take (inc current-frame-idx) frames)]
                    (let [bitmap (:bitmap frame)]
                      (when bitmap
                        (draw-bitmap ctx bitmap))))
                  (reset! last-rendered-idx current-frame-idx))
                ;; Forward playback - draw only new frames since last rendered
                (do
                  (doseq [frame (subvec frames (inc @last-rendered-idx) (inc current-frame-idx))]
                    (let [bitmap (:bitmap frame)]
                      (when bitmap
                        (draw-bitmap ctx bitmap))))
                  (reset! last-rendered-idx current-frame-idx)))))))
      :reagent-render
      (fn [_ canvas-width canvas-height _]
        [:> Box {:class "rdp-canvas-container relative bg-[--gray-9] rounded-lg flex items-center justify-center"
                 :style {:height "600px" :width "100%"}}
         [:canvas {:ref #(reset! canvas-ref %)
                   :width canvas-width
                   :height canvas-height
                   :style {:maxWidth "100%"
                           :maxHeight "100%"
                           :objectFit "contain"}}]])})))

;; Find frame index for a given timestamp
(defn- find-frame-index-for-time [frames target-time]
  (loop [idx 0
         best-idx 0]
    (if (>= idx (count frames))
      best-idx
      (let [frame (nth frames idx)
            ts (:timestamp frame)]
        (if (<= ts target-time)
          (recur (inc idx) idx)
          best-idx)))))

;; Main RDP player with streaming - timestamp-based playback
(defn- rdp-streaming-player [session-id canvas-width canvas-height initial-total-frames]
  (let [state (r/atom {:frames []
                       :current-frame 0
                       :playing false
                       :loading false
                       :loaded-up-to 0
                       :total-frames initial-total-frames
                       :total-duration 0
                       :playback-speed 1
                       :fetching false
                       :fullscreen false
                       ;; Track virtual time for playback
                       :start-time nil      ;; Real time when playback started
                       :start-offset 0})    ;; Time offset when playback started
        raf-id (r/atom nil)
        container-ref (r/atom nil)
        fullscreen-listener (r/atom nil)]
    (r/create-class
     {:display-name "rdp-streaming-player"
      :component-did-mount
      (fn []
        ;; Ensure RLE decompressor is loaded
        (ensure-rle-loaded)
        ;; Add fullscreen styles
        (ensure-fullscreen-styles)
        ;; Listen to fullscreen changes
        (let [listener (fn []
                         (swap! state assoc :fullscreen (boolean (.-fullscreenElement js/document))))]
          (reset! fullscreen-listener listener)
          (.addEventListener js/document "fullscreenchange" listener))
        ;; Load initial batch of frames
        (swap! state assoc :loading true)
        (fetch-frames
         session-id 0 100
         (fn [data]
           (swap! state assoc
                  :frames (:frames data)
                  :loaded-up-to (count (:frames data))
                  :loading false
                  :total-frames (:total_frames data)
                  :total-duration (:total_duration data)))
         (fn [err]
           (js/console.error "Failed to load frames:" err)
           (swap! state assoc :loading false))))

      :component-will-unmount
      (fn []
        (when @raf-id
          (js/cancelAnimationFrame @raf-id))
        ;; Remove fullscreen listener
        (when @fullscreen-listener
          (.removeEventListener js/document "fullscreenchange" @fullscreen-listener)))

      :reagent-render
      (fn []
        (let [{:keys [frames current-frame playing loading _loaded-up-to total-frames total-duration playback-speed start-time start-offset fetching fullscreen]} @state
              ;; Calculate current time position
              current-time (if playing
                             (+ start-offset (* (- (js/performance.now) start-time) playback-speed 0.001))
                             (if (seq frames)
                               (:timestamp (nth frames current-frame 0))
                               0))
              toggle-fullscreen (fn []
                                  (if fullscreen
                                    (when (.-exitFullscreen js/document)
                                      (.exitFullscreen js/document))
                                    (when-let [container @container-ref]
                                      (when (.-requestFullscreen container)
                                        (.requestFullscreen container))))
                                  (swap! state update :fullscreen not))]
          [:> Box {:ref #(reset! container-ref %)
                   :class "rdp-player-container flex flex-col space-y-radix-4"
                   :style {:height "660px"}}
           ;; Canvas - pass current-frame so it knows what to render
           [:> Box {:class "flex-1 overflow-hidden relative"}
            [canvas-renderer frames canvas-width canvas-height current-frame]
            ;; Fetching more frames indicator
            (when fetching
              [:> Box {:class "absolute top-2 right-2"}
               [:> Badge {:color "blue" :variant "soft"}
                "Loading more frames..."]])]
           ;; Controls
           [:> Box {:class "playback-controls-container flex-shrink-0"}
            [playback-controls
             {:playing? playing
              :fullscreen? fullscreen
              :on-fullscreen toggle-fullscreen
              :on-play (fn []
                         (swap! state assoc
                                :playing true
                                :start-time (js/performance.now)
                                :start-offset (if (seq frames)
                                                (:timestamp (nth frames current-frame 0))
                                                0))
                         ;; Timestamp-based playback loop using requestAnimationFrame
                         (letfn [(play-tick []
                                   (when (:playing @state)
                                     (let [frames (:frames @state)
                                           speed (:playback-speed @state)
                                           st (:start-time @state)
                                           so (:start-offset @state)
                                           td (:total-duration @state)
                                           current-time (+ so (* (- (js/performance.now) st) speed 0.001))]
                                       (if (>= current-time td)
                                         ;; Reached the end - show last frame and stop
                                         (swap! state assoc
                                                :current-frame (max 0 (dec (count frames)))
                                                :playing false)
                                         ;; Still playing
                                         (do
                                           (let [next-frame-idx (find-frame-index-for-time frames current-time)]
                                             ;; Only update if frame changed
                                             (when (not= next-frame-idx (:current-frame @state))
                                               (swap! state assoc :current-frame next-frame-idx))
                                             ;; Check if we need to load more frames
                                             (when (and (>= next-frame-idx (- (:loaded-up-to @state) 10))
                                                        (< (:loaded-up-to @state) (:total-frames @state))
                                                        (not (:fetching @state)))
                                               (swap! state assoc :fetching true)
                                               (fetch-frames
                                                session-id (:loaded-up-to @state) 200
                                                (fn [data]
                                                  (swap! state #(-> %
                                                                    (update :frames into (:frames data))
                                                                    (update :loaded-up-to + (count (:frames data)))
                                                                    (assoc :fetching false))))
                                                (fn [err]
                                                  (js/console.error "Failed to load more frames:" err)
                                                  (swap! state assoc :fetching false)))))
                                           ;; Schedule next tick
                                           (reset! raf-id (js/requestAnimationFrame play-tick)))))))]
                           (play-tick)))
              :on-pause (fn []
                          (swap! state assoc :playing false)
                          (when @raf-id
                            (js/cancelAnimationFrame @raf-id)))
              :on-prev (fn []
                         (when (> current-frame 0)
                           (swap! state update :current-frame dec)))
              :on-next (fn []
                         (when (< current-frame (dec total-frames))
                           (swap! state update :current-frame inc)))
              :current-time current-time
              :total-duration total-duration
              :playback-speed playback-speed
              :on-speed-change (fn [speed]
                                 (swap! state assoc :playback-speed speed))
              :on-seek (fn [target-time]
                         ;; Find frame for target time
                         (let [target-frame (find-frame-index-for-time frames target-time)]
                           (swap! state assoc
                                  :current-frame target-frame
                                  :start-offset target-time
                                  :start-time (when playing (js/performance.now)))
                           ;; Load frames if seeking beyond what we have
                           (when (> target-frame (:loaded-up-to @state))
                             (fetch-frames
                              session-id target-frame 100
                              (fn [data]
                                (swap! state assoc :frames (:frames data))
                                (swap! state assoc :loaded-up-to (+ target-frame (count (:frames data)))))
                              (fn [err]
                                (js/console.error "Failed to load frames:" err))))))}]]
           ;; Loading indicator
           (when loading
             [:> Flex {:justify "center" :align "center" :gap "2" :class "py-2"}
              [loaders/simple-loader {:size 4}]
              [:> Badge {:color "blue" :variant "soft"}
               "Loading frames..."]])]))})))

;; RDP session info display when event_stream is not loaded (large payload)
(defn- rdp-session-summary [{:keys [_session-id bitmap-count event-size canvas-width canvas-height on-play-click]}]
  [:> Flex {:direction "column"
            :align "center"
            :justify "center"
            :gap "4"
            :class "py-large"}
   [:> Box {:class "text-center"}
    [:> Heading {:size "6" :weight "bold" :class "text-[--gray-12]"}
     "RDP Session Recording"]
    [:> Flex {:gap "2" :align "center" :justify "center" :class "mt-2"}
     [:> Text {:size "3" :class "text-[--gray-11]"}
      (str bitmap-count " bitmap frames recorded")]
     [:> Text {:size "3" :class "text-[--gray-11]"} "•"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      (str (.toFixed (/ event-size 1024 1024) 2) " MB data")]
     [:> Text {:size "3" :class "text-[--gray-11]"} "•"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      (str canvas-width "x" canvas-height " resolution")]]]
   [:> Text {:size "2" :class "text-[--gray-11]"}
    "RDP session recordings are stored as bitmap frames for replay."]
   [:> Box {:class "mt-4"}
    [:> Button {:size "3"
                :on-click on-play-click}
     [:> Play {:size 16}]
     "Play Recording"]]])

(defn main [_event-stream session-id metrics]
  (let [show-player (r/atom false)]
    (fn []
      (let [bitmap-count (get-in metrics [:bitmap_count] 0)
            event-size (get-in metrics [:event_size] 0)
            canvas-width (get-in metrics [:canvas_width] 1280)
            canvas-height (get-in metrics [:canvas_height] 720)]
        [:> Box {:class "space-y-radix-5" :style {:maxWidth "100%" :overflow "hidden"}}
         (cond
           ;; Show streaming player when user clicks play
           @show-player
           [rdp-streaming-player session-id canvas-width canvas-height bitmap-count]

           ;; Has bitmap data in metrics
           (> bitmap-count 0)
           [rdp-session-summary {:session-id session-id
                                 :bitmap-count bitmap-count
                                 :event-size event-size
                                 :canvas-width canvas-width
                                 :canvas-height canvas-height
                                 :on-play-click #(reset! show-player true)}]

           ;; No data
           :else
           [empty-event-stream/main])]))))
