(ns webapp.components.charts
  (:require [recharts :as recharts]
            [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.utilities :refer [cn]]
            [cljs.core :as c]))

(def themes {:light "" :dark ".dark"})

(def chart-context (r/atom {}))

(defn chart-style [{:keys [id config]}]
  (let [color-config (filter (fn [[_ item-config]]
                               (or (:theme item-config) (:color item-config)))
                             config)
        chart-id (name id)]
    (when (seq color-config)
      [:style
       {:dangerouslySetInnerHTML
        {:__html (cs/join "\n"
                          (map (fn [[theme prefix]]
                                 (str prefix " [data-chart=" chart-id "] {\n"
                                      (cs/join "\n"
                                               (map (fn [[key item-config]]
                                                      (let [color (or (get-in item-config [:theme theme])
                                                                      (:color item-config))]
                                                        (when color
                                                          (str "  --color-" (name key) ": " color ";"))))
                                                    color-config))
                                      "\n}"))
                               themes))}}])))

(defn chart-container [{:keys [id class-name config chartid children] :as props}]
  (swap! chart-context assoc chartid {:config config})
    ;; (reset! chart-context {:config config})
  [:div
   (merge
    {:data-chart chartid
     :ref (.-ref props)
     :class (cn
             "flex aspect-video justify-center text-xs"
             "[&_.recharts-cartesian-axis-tick_text]:fill-muted-foreground"
             "[&_.recharts-cartesian-grid_line[stroke='#ccc']]:stroke-border/50"
             "[&_.recharts-curve.recharts-tooltip-cursor]:stroke-border"
             "[&_.recharts-dot[stroke='#fff']]:stroke-transparent"
             "[&_.recharts-layer]:outline-none"
             "[&_.recharts-polar-grid_[stroke='#ccc']]:stroke-border"
             "[&_.recharts-radial-bar-background-sector]:fill-muted"
             "[&_.recharts-rectangle.recharts-tooltip-cursor]:fill-muted"
             "[&_.recharts-reference-line_[stroke='#ccc']]:stroke-border"
             "[&_.recharts-sector[stroke='#fff']]:stroke-transparent"
             "[&_.recharts-sector]:outline-none"
             "[&_.recharts-surface]:outline-none"
             class-name)}
    (dissoc props :id :class-name :children :config))
   [chart-style {:id chartid :config config}]
   [:> recharts/ResponsiveContainer children]])

(defn get-payload-config-from-payload [config payload key]
  (let [payload-payload (:payload payload)]
    (cond
      (contains? payload (keyword key)) (get config (get payload (keyword key)))
      (and payload-payload (contains? payload-payload (keyword key))) (get config (keyword key))
      :else (get config (keyword key)))))

(defn chart-tooltip-content
  [{:keys [chartid active payload class-name indicator hide-label hide-indicator label label-formatter label-class-name formatter name-key label-key]}]
  (let [config (:config (get @chart-context chartid))
        tooltip-label (fn []
                        (when-not (or hide-label (empty? payload))
                          (let [{:keys [dataKey name] :as item} (first payload)
                                key (or label-key dataKey name "value")
                                item-config (get-payload-config-from-payload config item key)
                                value (or (when (and (nil? label-key) (string? label))
                                            (get-in config [label :label]))
                                          (:label item-config))]
                            (cond
                              label-formatter [:div {:class (cn "font-medium" label-class-name)}
                                               (label-formatter value payload)]
                              value [:div {:class (cn "font-medium" label-class-name)} value]))))
        nest-label (= 1 (count payload) (not= "dot" indicator))]
    (when (and active (seq payload))
      [:div
       {:class (cn
                "grid min-w-[8rem] items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl"
                class-name)}
       (when-not nest-label [tooltip-label])
       [:div {:class "grid gap-1.5"}
        (map-indexed
         (fn [index {:keys [dataKey name payload fill color] :as item}]
           (let [key (or name-key name dataKey "value")
                 item-config (get-payload-config-from-payload config item key)
                 indicator-color (or color (-> item :payload :fill) (-> item :color) fill)]
             [:div
              {:key dataKey
               :class (cn
                       "flex w-full flex-wrap items-stretch gap-2"
                       (when (= "dot" indicator) "items-center"))}
              (if formatter
                (formatter (:value item) name item index payload)
                [:<>
                 (when (and (nil? (:icon item-config)) (not hide-indicator))
                   [:div {:class (cn
                                  "shrink-0 rounded-[2px] border-[--color-border] bg-[--color-bg]"
                                  (cond
                                    (= "dot" indicator) "h-2.5 w-2.5"
                                    (= "line" indicator) "w-1"
                                    (= "dashed" indicator) "w-0 border-[1.5px] border-dashed bg-transparent"
                                    nest-label "my-0.5"))
                          :style {"--color-bg" indicator-color "--color-border" indicator-color}}])
                 [:div {:class (cn "flex flex-1 justify-between leading-none"
                                   (if nest-label "items-end" "items-center"))}
                  [:div {:class "grid gap-1.5 text-black"}
                   (when nest-label [tooltip-label])
                   [:span {:class "text-muted-foreground pr-2"}
                    (:label item-config)]]
                  (when (:value item)
                    [:span {:class "font-mono font-medium tabular-nums text-foreground"}
                     (.toLocaleString (:value item))])]])]))
         payload)]])))

(defn chart-legend-content
  [{:keys [chartid class-name hide-icon payload vertical-align name-key]}]
  (let [config (:config (get @chart-context chartid))]
    (when (seq payload)
      [:div
       {:class (cn
                "flex items-center justify-center gap-4"
                (if (= vertical-align "top") "pb-3" "pt-3")
                class-name)}
       (map
        (fn [{:keys [value dataKey color]}]
          (let [key (or name-key dataKey "value")
                item-config (get-payload-config-from-payload config {:dataKey dataKey} key)]
            [:div
             {:key value
              :class "flex items-center gap-1.5"}
             (if (and (:icon item-config) (not hide-icon))
               [:> (:icon item-config)]
               [:div {:class "h-2 w-2 shrink-0 rounded-[2px]" :style {:background-color color}}])
             (:label item-config)]))
        payload)])))
