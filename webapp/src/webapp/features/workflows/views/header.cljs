(ns webapp.features.workflows.views.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex IconButton Text Tooltip]]
   ["lucide-react" :refer [ArrowLeft Check CircleCheckBig Clock2 Copy
                           Link2 OctagonX Workflow]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.formatters :as formatters]))

;; ─── Status presentation ─────────────────────────────────────────────────────

(defn- status-token
  "Minimal status descriptor used across the hero. Returns
   [accent-color icon-component label color-css]"
  [status]
  (case status
    :running [:yellow Clock2          "In flight"  "var(--yellow-9)"]
    :error   [:red    OctagonX        "Failed"     "var(--red-9)"]
    :success [:green  CircleCheckBig  "Succeeded"  "var(--green-9)"]
    :empty   [:gray   Workflow        "Empty"      "var(--gray-9)"]
    [:gray Workflow "Unknown" "var(--gray-9)"]))

(defn- status-eyebrow
  "Small caps status label that sits above the hero duration. Includes
   a glowing indicator dot for the active state. The dot is the only
   chromatic accent in the editorial monochrome treatment."
  [status]
  (let [[_ _ label color] (status-token status)]
    [:> Flex {:align "center" :gap "3"}
     [:> Box {:class (str "relative flex items-center justify-center "
                          (when (= status :running) "animate-pulse"))}
      ;; outer halo
      [:> Box {:class "absolute inset-0 rounded-full blur-md opacity-60"
               :style {:backgroundColor color}}]
      ;; solid dot
      [:> Box {:class "relative w-2.5 h-2.5 rounded-full"
               :style {:backgroundColor color
                       :boxShadow (str "0 0 0 3px rgba(255,255,255,0.06), "
                                       "0 0 12px " color)}}]]
     [:> Text {:size "1" :weight "bold"
               :class "uppercase tracking-[0.18em] text-white/80"}
      label]]))

;; ─── Atomic pieces ──────────────────────────────────────────────────────────

(defn- copy-button
  "Inline copy affordance for the correlation id."
  [text]
  (let [copied? (r/atom false)]
    (fn [text]
      [:> Tooltip {:content (if @copied? "Copied" "Copy correlation ID")}
       [:> IconButton
        {:size "1"
         :variant "ghost"
         :color "gray"
         :class "text-white/70 hover:text-white"
         :on-click (fn []
                     (-> js/navigator .-clipboard (.writeText text)
                         (.then (fn []
                                  (reset! copied? true)
                                  (js/setTimeout #(reset! copied? false) 1500)))))}
        (if @copied?
          [:> Check {:size 14}]
          [:> Copy {:size 14}])]])))

(defn- share-button []
  (let [copied? (r/atom false)]
    (fn []
      [:> Button
       {:size "2"
        :variant "soft"
        :color "gray"
        :class (str "bg-white/10 text-white hover:bg-white/15 "
                    "border border-white/10 backdrop-blur-sm")
        :on-click (fn []
                    (-> js/navigator
                        .-clipboard
                        (.writeText (.. js/window -location -href))
                        (.then (fn []
                                 (reset! copied? true)
                                 (rf/dispatch [:show-snackbar
                                               {:level :success
                                                :text "Workflow link copied"}])
                                 (js/setTimeout #(reset! copied? false) 1500)))))}
       (if @copied?
         [:> Check {:size 14}]
         [:> Link2 {:size 14}])
       [:> Text {:size "2" :weight "medium"}
        (if @copied? "Copied" "Share")]])))

;; ─── Health bar (success/error/running ratio) ───────────────────────────────

(defn- health-bar
  "Horizontal stacked bar showing the breakdown of step outcomes.
   Reads as a single, glanceable signal of the workflow's health."
  [{:keys [steps success-count error-count running-count]}]
  (let [steps' (max 1 steps)
        seg (fn [count color]
              (when (pos? count)
                [:> Box {:class "h-full"
                         :style {:width (str (* 100.0 (/ count steps')) "%")
                                 :backgroundColor color}}]))]
    [:> Flex {:direction "column" :gap "2" :class "w-full"}
     [:> Box {:class (str "h-1.5 w-full overflow-hidden rounded-full "
                          "bg-white/10 flex")}
      (seg success-count "var(--green-9)")
      (seg error-count   "var(--red-9)")
      (seg running-count "var(--yellow-9)")]
     [:> Flex {:align "center" :gap "4" :class "text-white/70"}
      (when (pos? success-count)
        [:> Flex {:align "center" :gap "2"}
         [:> Box {:class "w-1.5 h-1.5 rounded-full"
                  :style {:backgroundColor "var(--green-9)"}}]
         [:> Text {:size "1" :class "tabular-nums"}
          (str success-count " ok")]])
      (when (pos? error-count)
        [:> Flex {:align "center" :gap "2"}
         [:> Box {:class "w-1.5 h-1.5 rounded-full"
                  :style {:backgroundColor "var(--red-9)"}}]
         [:> Text {:size "1" :class "tabular-nums"}
          (str error-count " failed")]])
      (when (pos? running-count)
        [:> Flex {:align "center" :gap "2"}
         [:> Box {:class "w-1.5 h-1.5 rounded-full animate-pulse"
                  :style {:backgroundColor "var(--yellow-9)"}}]
         [:> Text {:size "1" :class "tabular-nums"}
          (str running-count " running")]])]]))

;; ─── Meta block ─────────────────────────────────────────────────────────────

(defn- meta-pair
  "label / value pair used in the bottom meta row. Stays subdued so the
   hero duration remains the loudest element on the card."
  [label value]
  [:> Flex {:direction "column" :gap "1" :class "min-w-0"}
   [:> Text {:size "1" :weight "medium"
             :class "uppercase tracking-[0.14em] text-white/45"}
    label]
   [:> Text {:size "2" :weight "medium"
             :class "text-white/90 truncate"}
    value]])

(defn- absolute-time [ms]
  (when ms
    (formatters/time-parsed->readable-datetime
     (.toISOString (js/Date. ms)))))

(defn- relative-time [ms]
  (when ms
    (formatters/time-ago-full-date
     (.toISOString (js/Date. ms)))))

(defn- identity-meta-value
  "Friendly identity rendering inside the meta row."
  [{:keys [identities machine?]}]
  (cond
    (zero? (count identities))
    "—"

    (= 1 (count identities))
    (str (first identities)
         (when machine? " · machine"))

    :else
    (str (count identities) " identities")))

;; ─── Hero card ──────────────────────────────────────────────────────────────

(defn- duration-display
  "Splits a compact duration like '4m 12s' into a large numeric portion and
   a smaller unit tail so we get an editorial scale on the centerpiece."
  [duration-str]
  (if (or (nil? duration-str) (= duration-str "—"))
    [:> Text {:class "text-[80px] leading-none font-bold text-white tracking-tight tabular-nums"}
     "—"]
    [:> Flex {:align "baseline" :gap "1" :class "tabular-nums"}
     [:> Text {:class "text-[80px] leading-none font-bold text-white tracking-tight"}
      duration-str]]))

(defn- hero-card
  "The dark editorial-monochrome card: subtle dotted background, single
   focal duration, eyebrow status, health bar, meta footer."
  [correlation-id summary]
  (let [identity-text (identity-meta-value summary)
        started-rel (or (relative-time (:start-ms summary)) "—")
        started-abs (absolute-time (:start-ms summary))]
    [:> Box
     {:class (str "relative overflow-hidden rounded-6 "
                  "border border-[--gray-a4] "
                  "shadow-[0_24px_60px_-30px_rgba(0,0,0,0.45)]")
      :style {:background
              (str "radial-gradient(120% 80% at 100% 0%, "
                   "rgba(255,255,255,0.06) 0%, transparent 55%), "
                   "linear-gradient(180deg, var(--gray-12) 0%, var(--gray-12) 60%, "
                   "color-mix(in srgb, var(--gray-12) 92%, var(--gray-11)) 100%)")}}

     ;; ── decorative dot grid ────────────────────────────────────────
     [:> Box {:aria-hidden "true"
              :class "absolute inset-0 pointer-events-none opacity-[0.18]"
              :style {:backgroundImage
                      "radial-gradient(circle at 1px 1px, rgba(255,255,255,0.55) 1px, transparent 0)"
                      :backgroundSize "14px 14px"
                      :maskImage
                      "radial-gradient(120% 100% at 0% 0%, black 30%, transparent 75%)"
                      :WebkitMaskImage
                      "radial-gradient(120% 100% at 0% 0%, black 30%, transparent 75%)"}}]

     ;; ── decorative conic glow ──────────────────────────────────────
     [:> Box {:aria-hidden "true"
              :class "absolute -right-20 -top-20 w-72 h-72 rounded-full opacity-30 pointer-events-none"
              :style {:background
                      "conic-gradient(from 200deg at 50% 50%, transparent, rgba(255,255,255,0.07), transparent 60%)"}}]

     ;; ── content ────────────────────────────────────────────────────
     [:> Flex {:direction "column"
               :class "relative z-10 p-7 sm:p-9"
               :gap "7"}

      ;; Top row: eyebrow status + share
      [:> Flex {:align "start" :justify "between" :gap "4"}
       [:> Flex {:direction "column" :gap "3" :class "min-w-0"}
        [status-eyebrow (:status summary)]
        [:> Flex {:align "center" :gap "2" :class "min-w-0"}
         [:> Text {:size "1" :weight "medium"
                   :class "uppercase tracking-[0.18em] text-white/45"}
          "Workflow"]
         [:> Box {:class "h-3 w-px bg-white/15"}]
         [:> Text {:class (str "font-mono text-[13px] text-white/85 truncate "
                               "min-w-0")
                   :title correlation-id}
          correlation-id]
         [copy-button correlation-id]]]
       [share-button]]

      ;; Hero metric: duration
      [:> Flex {:align "end" :justify "between" :gap "6" :wrap "wrap"}
       [:> Flex {:direction "column" :gap "2" :class "min-w-0"}
        [:> Text {:size "1" :weight "medium"
                  :class "uppercase tracking-[0.18em] text-white/45"}
         "Total duration"]
        [duration-display (formatters/duration-ms->compact (:duration-ms summary))]
        [:> Text {:size "2" :class "text-white/55 tabular-nums"}
         (let [steps (:steps summary)]
           (str steps " " (if (= 1 steps) "step" "steps")
                " · started " started-rel))]]

       ;; Health bar — fills the right side of the hero
       [:> Box {:class "min-w-[240px] sm:min-w-[300px] grow max-w-md"}
        [health-bar summary]]]

      ;; Subtle separator
      [:> Box {:class "h-px w-full bg-white/10"}]

      ;; Meta footer: identity, started absolute, errors
      [:> Flex {:gap "8" :wrap "wrap"}
       [meta-pair "Initiator" identity-text]
       [meta-pair "Started"   (or started-abs "—")]
       [meta-pair "Errors"    (str (:error-count summary))]
       [meta-pair "Succeeded" (str (:success-count summary))]
       (when (pos? (:running-count summary))
         [meta-pair "In flight" (str (:running-count summary))])]]]))

;; ─── Top breadcrumb ─────────────────────────────────────────────────────────

(defn- breadcrumb []
  [:> Flex {:align "center" :gap "2"}
   [:> Tooltip {:content "Back to sessions"}
    [:> IconButton {:size "2" :variant "ghost" :color "gray"
                    :on-click #(rf/dispatch [:navigate :sessions])}
     [:> ArrowLeft {:size 16}]]]
   [:> Flex {:align "center" :gap "1"}
    [:> Text {:size "2" :weight "medium" :class "text-[--gray-11]"
              :as "button"
              :role "link"
              :style {:cursor "pointer"}
              :on-click #(rf/dispatch [:navigate :sessions])}
     "Sessions"]
    [:> Text {:size "2" :class "text-[--gray-9]"} "/"]
    [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
     "Workflow timeline"]]])

;; ─── Public entry ───────────────────────────────────────────────────────────

(defn header
  "Top section of the workflow page: breadcrumb + hero card."
  [correlation-id summary]
  [:> Flex {:direction "column" :gap "5"}
   [breadcrumb]
   [hero-card correlation-id summary]])
