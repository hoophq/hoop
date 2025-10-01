(ns webapp.features.runbooks.runner.views.list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   ["lucide-react" :refer [ChevronDown ChevronUp File Folder FolderOpen]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.tooltip :as tooltip]
   [webapp.config :as config]))

(defn sort-tree [data]
  (let [folders (->> data
                     (filter (fn [[k v]] (map? v)))
                     (sort-by first))
        files-or-list (->> data
                           (filter (fn [[k v]] (not (map? v))))
                           (sort-by first))]
    (into {} (concat folders files-or-list))))

(defn split-path [path]
  (cs/split path #"/"))

(defn is-file? [item]
  (or (string? item)
      (and (vector? item)
           (every? string? item))))

(defn insert-into-tree [tree path original-name]
  (if (empty? (rest path))
    (update tree (first path) (fnil conj []) original-name)
    (let [dir (first path)
          rest-path (rest path)
          sub-tree (get tree dir {})]
      (assoc tree dir (insert-into-tree sub-tree rest-path original-name)))))

(defn transform-payload [payload]
  (let [grouped-by-type (group-by
                         (fn [{:keys [name]}]
                           (if (cs/includes? name "/")
                             :with-folder
                             :without-folder))
                         payload)

        folder-tree (reduce
                     (fn [tree {:keys [name]}]
                       (let [path-parts (split-path name)]
                         (insert-into-tree tree path-parts name)))
                     {}
                     (:with-folder grouped-by-type))

        root-files (map (fn [{:keys [name]}]
                          [name name])
                        (:without-folder grouped-by-type))]

    (into folder-tree root-files)))

(defn file [filename filter-template-selected level selected?]
  [:> Flex {:class (str "items-center gap-2 pb-3 hover:underline cursor-pointer text-xs text-gray-12 whitespace-pre overflow-x-hidden "
             (when (pos? level) "pl-4"))
       :on-click (fn []
             (let [template (filter-template-selected filename)]
               (rf/dispatch [:runbooks/set-active-runbook template])))}
    [:> Flex {:class (str "w-fit gap-2 items-center py-1.5 px-2" (when selected?  " bg-[--indigo-a3] rounded-2"))} 
    [:> File {:size 16
          :class "text-[--gray-11]"}]
    [:> Text {:size "2" :weight "medium" :class "flex items-center block truncate"}
     [tooltip/truncate-tooltip {:text (last (split-path filename))}]]]])

(defn directory []
  (let [dropdown-status (r/atom {})
        search-term (rf/subscribe [:search/term])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])]

    (fn [name items level filter-template-selected parent-path]
      (let [current-path (if parent-path (str parent-path "/" name) name)]
        (when (and (seq @search-term)
                   (not= (get @dropdown-status name) :open))
          (swap! dropdown-status assoc name :open))

        (if (is-file? items)
          [file current-path filter-template-selected level (= current-path (get-in @selected-template [:data :name]))]
          [:> Box {:class (str "text-xs text-gray-12 " (when (pos? level) "pl-4"))}
           [:> Flex {:class "flex pb-4 items-center gap-small cursor-pointer"
                     :on-click #(swap! dropdown-status
                                       assoc-in [name]
                                       (if (= (get @dropdown-status name) :open) :closed :open))}
            (if (= (get @dropdown-status name) :open)
              [:> FolderOpen {:size 16
                              :class "text-[--gray-11]"}]
              [:> Folder {:size 16
                          :class "text-[--gray-11]"}])
            [:> Text {:size "2" :weight "medium" :class "hover:underline"} name]
            [:> Box
             (if (= (get @dropdown-status name) :open)
               [:> ChevronUp {:size 16
                              :class "text-[--gray-11]"}]
               [:> ChevronDown {:size 16
                                :class "text-[--gray-11]"}])]]

           [:> Box {:class (when (not= (get @dropdown-status name) :open)
                             "h-0 overflow-hidden")}
            (if (map? items)
              (let [subfolders-and-files (group-by (fn [[_ subcontents]]
                                                     (if (or (map? subcontents)
                                                             (and (vector? subcontents)
                                                                  (some map? subcontents)))
                                                       :folders
                                                       :files))
                                                   items)

                    sorted-subfolders (->> (get subfolders-and-files :folders {})
                                           (sort-by first))
                    sorted-files (->> (get subfolders-and-files :files {})
                                      (sort-by first))
                    child-level (inc level)]
                [:<>
                 (for [[subdirname subcontents] sorted-subfolders]
                   ^{:key subdirname}
                   [directory subdirname subcontents child-level filter-template-selected current-path])

                 (for [[filename subcontents] sorted-files]
                   ^{:key filename}
                   [directory filename subcontents child-level filter-template-selected current-path])])

              (for [item items]
                ^{:key item}
                [file item filter-template-selected (inc level) (= item (get-in @selected-template [:data :name]))]))]])))))

(defn directory-tree [tree filter-template-selected]
  (let [folders-and-files (group-by (fn [[_ contents]]
                                      (if (or (map? contents)
                                              (and (vector? contents)
                                                   (some map? contents)))
                                        :folders
                                        :files))
                                    tree)
        sorted-folders (->> (get folders-and-files :folders [])
                            (sort-by first))
        sorted-files (->> (get folders-and-files :files [])
                          (sort-by first))]

    [:> Box
     (for [[dirname contents] sorted-folders]
       ^{:key dirname}
       [directory dirname contents 0 filter-template-selected nil])

     (for [[filename contents] sorted-files]
       ^{:key filename}
       [directory filename contents 0 filter-template-selected nil])]))

(defn- loading-list-view []
  [:> Flex {:class "h-full text-center flex-col justify-center items-center"}
   [:> Text {:size "1" :class "text-gray-8"}
    "Loading runbooks"]
   [:> Box {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
    [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]])
    
(defn- empty-templates-view []
  [:> Flex {:class "h-full text-center flex-col justify-center items-center"}
   [:> Text {:size "1" :class "text-gray-8"}
    "No Runbooks available"]
   [:> Text {:size "1" :class "text-gray-8"}
    "Contact your Admin for more information"]])

(defn- no-integration-templates-view []
  [:> Flex {:class "h-full text-center flex-col justify-center items-center" :gap "4"}
   [:> Text {:size "1" :class "text-gray-8"}
    "No Runbooks configured on your Organization yet"]
   [:> Button {:color "indigo"
               :size "2"
               :variant "soft"
               :radius "medium"
               :on-click #(rf/dispatch [:navigate :runbooks-setup {:tab "configurations"}])}
    "Go to Runbooks Configuration"]])

(defn main []
  (fn [templates filtered-templates]
    (let [filter-template-selected (fn [template-name]
                                     (first (filter #(= (:name %) template-name) (:data @templates))))
          search-term (rf/subscribe [:search/term])
          templates-data (:data @templates)
          filtered-data (or @filtered-templates [])
          has-search? (seq @search-term)
          display-templates (if has-search?
                              filtered-data
                              (or templates-data []))
          transformed-payload (sort-tree (transform-payload display-templates))]

      (cond
        (= :loading (:status @templates)) [loading-list-view]
        (= :error (:status @templates)) [no-integration-templates-view]
        (and (empty? templates-data) (= :ready (:status @templates))) [empty-templates-view]
        (empty? display-templates) [:> Flex {:class "pt-2 text-center flex-col justify-center items-center" :gap "4"}
                                    [:> Text {:size "1" :class "text-gray-8"}
                                     (if has-search?
                                       (str "No runbooks matching \"" @search-term "\".")
                                       "There are no runbooks available.")]]
        :else [directory-tree transformed-payload filter-template-selected]))))
