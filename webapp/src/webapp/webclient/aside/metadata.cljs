(ns webapp.webclient.aside.metadata
  (:require [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]))

(defn metadata->add-new
  [config-map config-key config-value]
  (if-not (or (empty? config-key) (empty? config-value))
    (swap! config-map conj {:key config-key :value config-value})
    nil))

(defn metadata->inputs [{:keys [key value]} index config]
  (let [key-val (r/atom key)
        value-val (r/atom value)
        save (fn [k v] (swap! config assoc-in [index k] v))]
    (fn []
      [:<>
       [forms/input-metadata {:on-change #(reset! key-val (-> % .-target .-value))
                              :placeholder "Insert a metadata name"
                              :on-blur #(save :key @key-val)
                              :value @key-val}]
       [forms/input-metadata {:on-change #(reset! value-val (-> % .-target .-value))
                              :placeholder "Insert a metadata value"
                              :on-blur #(save :value @value-val)
                              :value @value-val}]])))

(defn metadata->json-stringify
  [metadata]
  (->> metadata
       (filter (fn [{:keys [key value]}]
                 (not (or (cs/blank? key) (cs/blank? value)))))
       (map (fn [{:keys [key value]}] {key value}))
       (reduce into {})
       (clj->js)))

(defn main [{:keys [metadata metadata-key metadata-value]}]
  (let [value->metadata-key @metadata-key
        value->metadata-value @metadata-value
        value->metadata @metadata
        onclick (fn []
                  (metadata->add-new metadata value->metadata-key value->metadata-value)
                  (reset! metadata-key "")
                  (reset! metadata-value ""))]
    [:<>
     [:div {:class "grid grid-cols-2 gap-small"}
      (for [index (range (count value->metadata))]
        ^{:key (:val (get value->metadata index))}
        [metadata->inputs (get value->metadata index) index metadata])

      [forms/input-metadata {:on-change #(reset! metadata-key (-> % .-target .-value))
                             :placeholder "Metadata name"
                             :value value->metadata-key}]
      [forms/input-metadata {:on-change #(reset! metadata-value (-> % .-target .-value))
                             :placeholder "Metadata value"
                             :value value->metadata-value}]]

     [:div {:class "mt-2 flex justify-center w-full"}
      (button/tailwind-secondary {:text "+ new metadata"
                                  :full-width true
                                  :on-click onclick
                                  :dark? true
                                  :variant :small})]]))
