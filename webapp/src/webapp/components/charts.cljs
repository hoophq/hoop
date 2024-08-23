(ns webapp.components.charts
  (:require ["react" :as react]
            ["recharts" :as recharts]
            ["unique-names-generator" :as ung]
            [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.utilities :refer [cn]]))

(def themes {:light "" :dark ".dark"})

(def chart-context (r/atom nil))

(defn random-connection-name []
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
        characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
                                                    :style "lowerCase"
                                                    :length 1})]
    (str characterName "-" numberDictionary)))

(defn use-chart []
  (let [context @chart-context]
    (when (nil? context)
      (throw (js/Error. "useChart must be used within a <ChartContainer />")))
    context))

(defn chart-style [{:keys [id config]}]
  (println config)
  (let [color-config (filter (fn [[_ item-config]]
                               (or (:theme item-config) (:color item-config)))
                             config)]
    (when (seq color-config)
      [:style
       {:dangerouslySetInnerHTML
        {:__html (cs/join "\n"
                          (map (fn [[theme prefix]]
                                 (str prefix " [data-chart=" id "] {\n"
                                      (cs/join "\n"
                                               (map (fn [[key item-config]]
                                                      (let [color (or (get-in item-config [:theme theme])
                                                                      (:color item-config))]
                                                        (when color
                                                          (str "  --color-" (name key) ": " color ";"))))
                                                    color-config))
                                      "\n}"))
                               themes))}}])))

(defn chart-container [{:keys [id class-name children config] :as props}]
  (let [unique-id (random-connection-name)
        chart-id (str "chart-" (or id (cs/replace unique-id ":" "")))]
    (reset! chart-context config)
    [:div
     (merge
      {:data-chart chart-id
       :ref (.-ref props)
       :class-name (cn
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
     [chart-style {:id chart-id :config config}]
     [:> recharts/ResponsiveContainer children]]))

(defn get-payload-config-from-payload [config payload key]
  (let [payload-payload (.-payload payload)]
    (cond
      (contains? payload key) (get config (get payload key))
      (and payload-payload (contains? payload-payload key)) (get config (get payload-payload key))
      :else (get config key))))

(defn chart-tooltip-content
  [{:keys [active payload class-name indicator hide-label hide-indicator label label-formatter label-class-name formatter color name-key label-key]}]
  (let [{:keys [config]} (use-chart)
        tooltip-label (react/useMemo
                       (fn []
                         (when-not (or hide-label (empty? payload))
                           (let [{:keys [dataKey name] :as item} (first payload)
                                 key (or label-key dataKey name "value")
                                 item-config (get-payload-config-from-payload config item key)
                                 value (or (when (and (nil? label-key) (string? label))
                                             (get-in config [label :label]))
                                           (:label item-config))]
                             (cond
                               label-formatter [:div {:class-name (cn "font-medium" label-class-name)}
                                                (label-formatter value payload)]
                               value [:div {:class-name (cn "font-medium" label-class-name)} value]))))
                       #js [label label-formatter payload hide-label label-class-name config label-key])
        nest-label (= 1 (count payload) (not= "dot" indicator))]
    (when (and active (seq payload))
      [:div
       {:class-name (cn
                     "grid min-w-[8rem] items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl"
                     class-name)}
       (when-not nest-label tooltip-label)
       [:div {:class-name "grid gap-1.5"}
        (map-indexed
         (fn [index {:keys [dataKey name payload fill color] :as item}]
           (let [key (or name-key name dataKey "value")
                 item-config (get-payload-config-from-payload config item key)
                 indicator-color (or color fill)]
             [:div
              {:key dataKey
               :class-name (cn
                            "flex w-full flex-wrap items-stretch gap-2"
                            (when (= "dot" indicator) "items-center"))}
              (if formatter
                (formatter (:value item) name item index payload)
                [:<>
                 (when (and (nil? (:icon item-config)) (not hide-indicator))
                   [:div
                    {:class-name (cn
                                  "shrink-0 rounded-[2px] border-[--color-border] bg-[--color-bg]"
                                  (cond
                                    (= "dot" indicator) "h-2.5 w-2.5"
                                    (= "line" indicator) "w-1"
                                    (= "dashed" indicator) "w-0 border-[1.5px] border-dashed bg-transparent"
                                    nest-label "my-0.5"))
                     :style {:--color-bg indicator-color :--color-border indicator-color}}])
                 [:div
                  {:class-name (cn "flex flex-1 justify-between leading-none"
                                   (if nest-label "items-end" "items-center"))}
                  [:div {:class-name "grid gap-1.5"}
                   (when nest-label tooltip-label)
                   [:span {:class-name "text-muted-foreground"} (:label item-config)]]
                  (when (:value item)
                    [:span {:class-name "font-mono font-medium tabular-nums text-foreground"}
                     (.toLocaleString (:value item))])]])]))
         payload)]])))

(defn chart-legend-content
  [{:keys [class-name hide-icon payload vertical-align name-key]}]
  (let [{:keys [config]} (use-chart)]
    (when (seq payload)
      [:div
       {:class-name (cn
                     "flex items-center justify-center gap-4"
                     (if (= vertical-align "top") "pb-3" "pt-3")
                     class-name)}
       (map
        (fn [{:keys [value dataKey color]}]
          (let [key (or name-key dataKey "value")
                item-config (get-payload-config-from-payload config {:dataKey dataKey} key)]
            [:div
             {:key value
              :class-name "flex items-center gap-1.5"}
             (if (and (:icon item-config) (not hide-icon))
               [:> (:icon item-config)]
               [:div {:class-name "h-2 w-2 shrink-0 rounded-[2px]" :style {:background-color color}}])
             (:label item-config)]))
        payload)])))
