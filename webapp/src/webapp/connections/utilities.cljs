(ns webapp.connections.utilities
  (:require [clojure.set :as set]
            [clojure.string :as s]
            [webapp.connections.constants :as constants]))

(defn normalize-key [k]
  (keyword (clojure.string/lower-case (name k))))

(defn merge-by-key [arr1 arr2]
  (let [map1 (into {} (map (fn [x] [(normalize-key (:key x)) x]) arr1))
        map2 (into {} (map (fn [x] [(normalize-key (:key x)) x]) arr2))
        all-keys (set/union (set (keys map1)) (set (keys map2)))]
    (mapv
     (fn [k]
       (let [val1 (:value (get map1 k))
             val2 (:value (get map2 k))
             required (:required (get map1 k))
             placeholder (:placeholder (get map1 k))
             hidden (:hidden (get map1 k))
             selected (cond
                        (and (not (empty? val1)) (empty? val2)) (get map1 k)
                        (and (empty? val1) (not (empty? val2))) (assoc (get map2 k)
                                                                       :required required
                                                                       :placeholder placeholder)
                        :else (if (nil? val1) (assoc (get map2 k)
                                                     :required required
                                                     :placeholder placeholder) (get map1 k)))]
         selected))
     all-keys)))

(defn get-config-keys
  [key]
  (get constants/connection-configs-required key))

(defn config->json
  [configs prefix]
  (->> configs
       (filter (fn [{:keys [key value]}]
                 (not (or (s/blank? key) (s/blank? value)))))
       (map (fn [{:keys [key value]}] {(str prefix (s/upper-case key)) (js/btoa value)}))
       (reduce into {})))

(defn json->config
  [configs]
  (if (or (s/blank? configs) (nil? configs))
    {}
    (->> configs
         (mapv (fn [[key value]] {:key (name key) :value value})))))

(defn separate-values-from-config-by-prefix
  [configs prefix]
  (let [regex (if (= prefix "envvar")
                #"envvar:"
                #"filesystem:")]
    (->> configs
         (filter (fn [[k]]
                   (s/includes? (name k) prefix)))
         (map (fn [[k v]]
                {(keyword (s/replace (name k) regex "")) (js/atob v)}))
         (reduce into {}))))
