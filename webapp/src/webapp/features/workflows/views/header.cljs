(ns webapp.features.workflows.views.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex IconButton Text Tooltip]]
   ["lucide-react" :refer [Check Copy Link2]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.formatters :as formatters]))

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

;; ─── Meta block ─────────────────────────────────────────────────────────────

(defn- meta-pair
  "label / value pair used in the summary row."
  [label value]
  [:> Flex {:direction "column" :gap "1" :class "min-w-0"}
   [:> Text {:size "1" :weight "medium"
             :class "uppercase tracking-wider text-[--gray-11]"}
    label]
   [:> Text {:size "2" :weight "medium" :class "text-[--gray-12] truncate"}
    value]])

(defn- absolute-time [ms]
  (when ms
    (formatters/time-parsed->readable-datetime
     (.toISOString (js/Date. ms)))))

;; ─── Summary panel ──────────────────────────────────────────────────────────

(defn- summary-panel
  "Light, compact summary that sits above the accordion list."
  [correlation-id summary]
  (let [started-abs (or (absolute-time (:start-ms summary)) "—")
        last-session-abs (or (absolute-time (:last-session-start-ms summary)) "—")
        sessions-count (:sessions-count summary)
        success-count (:success-count summary)
        error-count (:error-count summary)
        running-count (:running-count summary)]
    [:> Box {:class (str "rounded-4 border border-[--gray-a4] bg-white "
                         "px-radix-5 py-radix-4")}
     [:> Flex {:direction "column" :gap "4"}
      ;; Top: correlation id + share
      [:> Flex {:align "center" :justify "between" :gap "3" :wrap "wrap"}
       [:> Flex {:align "center" :gap "2" :class "min-w-0"}
        [:> Text {:size "1" :weight "bold"
                  :class "uppercase tracking-wider text-[--gray-11]"}
         "Correlation ID"]
        [:> Text {:class "font-mono text-[13px] text-[--gray-12] truncate"
                  :title correlation-id}
         correlation-id]
        [copy-button correlation-id]]
       [share-button]]

      ;; Meta row
      [:> Flex {:gap "8" :wrap "wrap"}
       [meta-pair "Sessions" (str sessions-count)]
       [meta-pair "Started" started-abs]
       [meta-pair "Last session at" last-session-abs]
       [meta-pair "Succeeded" (str success-count)]
       (when (pos? error-count)
         [meta-pair "Errors" (str error-count)])
       (when (pos? running-count)
         [meta-pair "In flight" (str running-count)])]]]))

;; ─── Public entry ───────────────────────────────────────────────────────────

(defn header
  "Compact summary panel above the accordion list."
  [correlation-id summary]
  [summary-panel correlation-id summary])
