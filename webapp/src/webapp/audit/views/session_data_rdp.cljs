(ns webapp.audit.views.session-data-rdp
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text Heading Badge]]
   ["lucide-react" :refer [Play Pause FastForward Rewind Maximize Minimize]]
   [reagent.core :as r]
   [webapp.audit.views.empty-event-stream :as empty-event-stream]
   [webapp.components.loaders :as loaders]
   [webapp.http.api :as api]))

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
        (let [bpp (or bits_per_pixel 16)
              ;; Decompress if needed
              pixel-data (if compressed
                           (decompress-rle data-bytes width height bpp)
                           data-bytes)
              ;; Use fast JS pixel conversion (handles bottom-up + BGR->RGB)
              rgba (js/window.rdpBitmapToRGBA pixel-data width height bpp)
              image-data (js/ImageData. rgba width height)]
          (.putImageData canvas-ctx image-data x y))
        (catch :default e
          (js/console.error "Error drawing bitmap:" e))))))

;; Fetch frames from API
(defn- fetch-frames [session-id offset limit on-success on-error]
  (api/request {:method "GET"
                :uri (str "/sessions/" session-id "/rdp-frames")
                :query-params {:offset offset :limit limit}
                :on-success on-success
                :on-failure on-error}))

(defn- fetch-detections [session-id on-success on-error]
  (api/request {:method "GET"
                :uri (str "/sessions/" session-id "/rdp-detections")
                :on-success on-success
                :on-failure on-error}))

(def ^:private entity-colors
  {"PERSON" {:fill "rgba(239, 68, 68, 0.25)" :stroke "rgba(239, 68, 68, 0.85)"}
   "EMAIL_ADDRESS" {:fill "rgba(249, 115, 22, 0.25)" :stroke "rgba(249, 115, 22, 0.85)"}
   "PHONE_NUMBER" {:fill "rgba(234, 179, 8, 0.25)" :stroke "rgba(234, 179, 8, 0.85)"}
   "URL" {:fill "rgba(59, 130, 246, 0.25)" :stroke "rgba(59, 130, 246, 0.85)"}
   "LOCATION" {:fill "rgba(34, 197, 94, 0.25)" :stroke "rgba(34, 197, 94, 0.85)"}
   "DATE_TIME" {:fill "rgba(168, 85, 247, 0.25)" :stroke "rgba(168, 85, 247, 0.85)"}
   "NRP" {:fill "rgba(14, 165, 233, 0.25)" :stroke "rgba(14, 165, 233, 0.85)"}})

(defn- get-entity-color [entity-type]
  (get entity-colors entity-type {:fill "rgba(107, 114, 128, 0.25)"
                                   :stroke "rgba(107, 114, 128, 0.85)"}))

(defn- group-detections-by-snapshot
  "Groups detections by their snapshot timestamp. Returns a sorted vector of
   [timestamp, detections] pairs for efficient binary search during playback."
  [detections]
  (let [by-ts (reduce (fn [acc detection]
                        (update acc (:timestamp detection) (fnil conj []) detection))
                      {}
                      detections)]
    (->> by-ts
         (sort-by first)
         vec)))

(defn- find-active-detections
  "Binary-search the sorted snapshot list to find detections for the most recent
   snapshot whose timestamp is <= current-time."
  [sorted-snapshots current-time]
  (when (seq sorted-snapshots)
    (loop [lo 0
           hi (dec (count sorted-snapshots))
           best nil]
      (if (> lo hi)
        best
        (let [mid (quot (+ lo hi) 2)
              [ts dets] (nth sorted-snapshots mid)]
          (if (<= ts current-time)
            (recur (inc mid) hi dets)
            (recur lo (dec mid) best)))))))

(defn- draw-overlay-detections [overlay-ctx detections enabled-entity-types]
  (doseq [{:keys [x y width height entity_type]} detections
          :when (and (> width 0)
                     (> height 0)
                     (contains? enabled-entity-types entity_type))]
    (let [{:keys [fill stroke]} (get-entity-color entity_type)]
      (set! (.-fillStyle overlay-ctx) fill)
      (set! (.-strokeStyle overlay-ctx) stroke)
      (set! (.-lineWidth overlay-ctx) 1)
      (.fillRect overlay-ctx x y width height)
      (.strokeRect overlay-ctx x y width height))))

(defn- entity-legend [{:keys [entity-types enabled-entity-types on-toggle detections-loading analysis-status]}]
  (when (or detections-loading (seq entity-types) (seq analysis-status))
    [:> Flex {:align "center"
              :gap "2"
              :wrap "wrap"
              :class "px-radix-4 py-radix-3 rounded-b-lg bg-[--gray-1] border-t border-[--gray-6]"}
     [:> Text {:size "1" :weight "medium" :class "uppercase tracking-[0.08em] text-[--gray-11]"}
      "PII highlights"]
     (when (seq analysis-status)
       [:> Badge {:variant "soft" :color "gray" :size "1"}
        (str "Status: " analysis-status)])
     (when detections-loading
       [:> Badge {:variant "soft" :color "blue" :size "1"}
        "Loading detections..."])
     (for [entity-type entity-types]
       (let [{:keys [fill stroke]} (get-entity-color entity-type)
             enabled? (contains? enabled-entity-types entity-type)]
         ^{:key entity-type}
         [:> Badge {:variant (if enabled? "solid" "soft")
                    :size "1"
                    :class "cursor-pointer select-none"
                    :on-click #(on-toggle entity-type)
                    :style {:border (str "1px solid " stroke)
                            :backgroundColor (if enabled? fill "transparent")
                            :color (if enabled? stroke "var(--gray-11)")
                            :opacity (if enabled? 1 0.6)}}
          entity-type]))]))

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
  [:> Box {:class "rdp-controls space-y-radix-2 p-radix-4 rounded-b-lg bg-[--gray-2]"
           :style {:height "90px"}}
   [progress-bar {:current-time current-time
                  :total-duration total-duration
                  :on-seek on-seek}]
   [:> Flex {:justify "between" :align "center"}
    [:> Flex {:gap "2" :align "center"}
     [:> Button {:variant "soft"
                 :size "2"
                 :color "gray"
                 :on-click on-prev}
      [:> Rewind {:size 16}]]
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
      [:> FastForward {:size 16}]]]
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
        overlay-canvas-ref (atom nil)
        ;; Track last rendered frame index to know what's new
        last-rendered-idx (atom -1)
        last-overlay-sig (atom nil)]
    (r/create-class
     {:display-name "rdp-canvas-renderer"
      :component-did-update
      (fn [this]
        (let [[_ frames canvas-width canvas-height current-frame-idx _fullscreen?
               current-time sorted-snapshots enabled-entity-types] (r/argv this)
              canvas @canvas-ref
              overlay-canvas @overlay-canvas-ref]
          (when (and canvas overlay-canvas (seq frames))
            (let [ctx (.getContext canvas "2d" (clj->js {:alpha false
                                                         :desynchronized true
                                                         :willReadFrequently false}))
                  overlay-ctx (.getContext overlay-canvas "2d" (clj->js {:alpha true
                                                                         :desynchronized true
                                                                         :willReadFrequently false}))
                  bitmap-changed? (not= current-frame-idx @last-rendered-idx)
                  ;; Find detections for the current playback time
                  active-detections (find-active-detections sorted-snapshots current-time)
                  overlay-sig [current-time enabled-entity-types (count sorted-snapshots)]
                  overlay-changed? (not= overlay-sig @last-overlay-sig)]
              ;; Disable image smoothing for better performance
              (set! (.-imageSmoothingEnabled ctx) false)
              ;; If seeking backwards or first render, clear and redraw from beginning
              (when bitmap-changed?
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
                    (reset! last-rendered-idx current-frame-idx))))

              (when (or bitmap-changed? overlay-changed?)
                (.clearRect overlay-ctx 0 0 canvas-width canvas-height)
                (when active-detections
                  (draw-overlay-detections overlay-ctx active-detections enabled-entity-types))
                (reset! last-overlay-sig overlay-sig))))))
       :reagent-render
       (fn [_ canvas-width canvas-height _ fullscreen? _ _ _]
         [:> Box {:class "rdp-canvas-container relative bg-[--gray-9] rounded-t-lg flex items-center justify-center"
                  :style (if fullscreen?
                           {:height "calc(100vh - 90px)" :width "100%"}
                           {:height "600px" :width "100%"})}
          ;; Main canvas — flex-centered, scales down via objectFit contain
          [:canvas {:ref #(reset! canvas-ref %)
                    :width canvas-width
                    :height canvas-height
                    :style {:maxWidth "100%"
                            :maxHeight "100%"
                            :objectFit "contain"}}]
          ;; Overlay canvas — absolutely positioned, centered with same scaling
          [:canvas {:ref #(reset! overlay-canvas-ref %)
                    :width canvas-width
                    :height canvas-height
                    :style {:position "absolute"
                            :top "50%"
                            :left "50%"
                            :transform "translate(-50%, -50%)"
                            :maxWidth "100%"
                            :maxHeight "100%"
                            :objectFit "contain"
                            :pointerEvents "none"}}]])})))

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
                        :detections-loading false
                        :loaded-up-to 0
                        :total-frames initial-total-frames
                        :total-duration 0
                        :playback-speed 1
                        :fetching false
                        :fullscreen false
                         :sorted-snapshots []
                         :entity-types []
                         :enabled-entity-types #{}
                         :analysis-status ""
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
                   :total-duration (:total_duration data)
                   :detections-loading true)
            (fetch-detections
             session-id
             (fn [response]
               (let [detections (:detections response)
                     entity-types (->> detections (map :entity_type) (remove nil?) set sort vec)]
                 (swap! state assoc
                        :sorted-snapshots (group-detections-by-snapshot detections)
                        :entity-types entity-types
                        :enabled-entity-types (set entity-types)
                        :analysis-status (:analysis_status response)
                        :detections-loading false)))
             (fn [err]
               (js/console.error "Failed to load detections:" err)
               (swap! state assoc :detections-loading false))))
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
         (let [{:keys [frames current-frame playing loading _loaded-up-to total-frames total-duration playback-speed start-time start-offset fetching fullscreen
                       sorted-snapshots entity-types enabled-entity-types detections-loading analysis-status]} @state
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
                    :class "rdp-player-container flex flex-col"}
            ;; Canvas - pass current-frame so it knows what to render
            [:> Box {:class "flex-1 relative"}
             [canvas-renderer frames canvas-width canvas-height current-frame fullscreen current-time sorted-snapshots enabled-entity-types]
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
            [entity-legend
             {:entity-types entity-types
              :enabled-entity-types enabled-entity-types
              :detections-loading detections-loading
              :analysis-status analysis-status
              :on-toggle (fn [entity-type]
                           (swap! state update :enabled-entity-types
                                  (fn [enabled]
                                    (if (contains? enabled entity-type)
                                      (disj enabled entity-type)
                                      (conj enabled entity-type)))))}]
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
