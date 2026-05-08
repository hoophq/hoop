(ns webapp.provisioning.views.shared
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading Text]]
   ["lucide-react" :refer [ArrowLeft Loader2 Upload]]))

(defn zebra-bg
  "Returns alternating row background for index i."
  [i]
  (if (even? i) "var(--color-panel-solid)" "var(--gray-1)"))

(defn bulk-screen-header
  "Back button + heading + resource count badge."
  [{:keys [title resource-count on-back]}]
  [:<>
   [:> Flex {:align "center" :gap "2" :mb "1"}
    [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-back}
     [:> ArrowLeft {:size 14}] " Back"]]
   [:> Flex {:align "baseline" :gap "3" :mb "5"}
    [:> Heading {:size "7"} title]
    [:> Badge {:color "gray" :variant "soft"} (str resource-count " resources")]]])

(defn info-callout
  "Radix Callout with an icon and a text message. Used for status banners."
  [{:keys [color icon text mb size]
    :or   {mb "4"}}]
  [:> Callout.Root (cond-> {:color color :mb mb}
                     size (assoc :size size))
   [:> Callout.Icon icon]
   [:> Callout.Text (if size {:size size} {}) text]])

(defn callout-bar
  "Color-coded banner with a left accent, leading icon, title/subtitle slot,
   and an actions slot on the right."
  [{:keys [color icon title subtitle extra actions px py]
    :or   {px "5" py "4"}}]
  (let [c #(str "var(--" color "-" % ")")]
    [:> Flex {:align "center" :justify "between" :gap "4"
              :px px :py py :mb "4"
              :style {:background    (c 2)
                      :border-top    (str "1px solid " (c 5))
                      :border-right  (str "1px solid " (c 5))
                      :border-bottom (str "1px solid " (c 5))
                      :border-left   (str "4px solid " (c 9))
                      :border-radius "var(--radius-3)"}}
     [:> Flex {:align "center" :gap "3"}
      (when icon
        [:> Box {:style {:color       (c 9)
                         :display     "flex"
                         :flex-shrink 0}}
         icon])
      [:> Flex {:direction "column" :gap "0"}
       (when title    [:> Text {:size "2" :weight "medium"} title])
       (when subtitle [:> Text {:size "1" :color "gray"} subtitle])
       extra]]
     (when actions
       [:> Flex {:align "center" :gap "2" :style {:flex-shrink 0}} actions])]))

(defn bulk-footer
  "Sticky footer: info text on the left, Cancel + primary action on the right."
  [{:keys [info-text on-cancel on-apply apply-disabled? apply-label]}]
  [:> Flex {:align "center" :justify "between" :pt "4" :mt "4"
            :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
   [:> Text {:size "1" :color "gray"} info-text]
   [:> Flex {:gap "3"}
    [:> Button {:variant "outline" :color "gray" :on-click on-cancel} "Cancel"]
    [:> Button {:disabled apply-disabled? :on-click on-apply} apply-label]]])

(defn csv-drop-zone
  "Dashed drop zone for CSV file selection. Handles click-to-browse and drag-and-drop.
   Props:
     :on-file      (fn [File]) — called when user selects or drops a file
     :hint-text    string      — column description shown below the icon
     :loading?     boolean     — show spinner instead of the icon
     :loading-text string      — text shown while loading (default \"Parsing CSV…\")"
  [{:keys [on-file hint-text loading? loading-text]}]
  [:> Box {:on-click (fn []
                       (when-not loading?
                         (let [input (js/document.createElement "input")]
                           (set! (.-type input) "file")
                           (set! (.-accept input) ".csv,text/csv")
                           (set! (.-onchange input)
                                 (fn [e]
                                   (when-let [file (-> e .-target .-files (aget 0))]
                                     (on-file file))))
                           (.click input))))
           :on-drop  (fn [e]
                       (.preventDefault e)
                       (when-not loading?
                         (when-let [file (-> e .-dataTransfer .-files (aget 0))]
                           (on-file file))))
           :on-drag-over #(.preventDefault %)
           :style {:border "2px dashed var(--gray-6)"
                   :border-radius "var(--radius-3)"
                   :padding 40 :background "var(--gray-2)"
                   :text-align "center" :cursor "pointer"
                   :flex 1 :display "flex" :align-items "center"
                   :justify-content "center"}}
   (if loading?
     [:> Flex {:direction "column" :align "center" :gap "2"}
      [:span {:class "animate-spin inline-flex" :style {:color "var(--indigo-9)"}}
       [:> Loader2 {:size 20}]]
      [:> Text {:size "2" :color "gray"} (or loading-text "Parsing CSV…")]]
     [:> Flex {:direction "column" :align "center" :gap "2"}
      [:> Upload {:size 24 :stroke-width 1.5 :color "var(--gray-9)"}]
      [:> Text {:size "2" :color "gray"}
       "Drop your CSV here or "
       [:> Text {:size "2" :color "indigo" :style {:cursor "pointer"}} "browse"]]
      (when hint-text
        [:> Text {:size "1" :color "gray"} hint-text])])])
